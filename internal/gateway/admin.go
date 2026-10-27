package gateway

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// Huma stays at the administrative HTTP boundary. Inference is handled directly
// by net/http; storage and accounting have no framework dependency.
type APIOutput[T any] struct{ Body T }
type Page[T any] struct {
	Data       []T    `json:"data"`
	NextCursor string `json:"next_cursor"`
}

func output[T any](v T) *APIOutput[T] { return &APIOutput[T]{Body: v} }
func paged[T any](data []T, limit int, id func(T) string) *APIOutput[Page[T]] {
	next := ""
	if len(data) == limit {
		next = id(data[len(data)-1])
	}
	return output(Page[T]{Data: data, NextCursor: next})
}

type Pagination struct {
	After string `query:"after" maxLength:"256" doc:"Cursor from the previous page; omit for the first page."`
	Limit int    `query:"limit" default:"100" minimum:"1" maximum:"500"`
}
type UserInput struct {
	UserID string `path:"user_id" minLength:"1" maxLength:"256" doc:"Exact immutable user ID, caller-supplied or gateway-generated, URL-encoded as a path segment."`
}
type RequestInput struct {
	Request string `path:"request" maxLength:"100"`
}
type UserPageInput struct {
	UserInput
	Pagination
}
type RequestPageInput struct {
	RequestInput
	Pagination
}

// optionalGroup distinguishes omission from explicit null.
type optionalGroup struct {
	Value string
	Set   bool
}

func (v *optionalGroup) UnmarshalJSON(data []byte) error {
	v.Set = true
	if string(data) == "null" {
		return errors.New("model_access_group cannot be null")
	}
	return json.Unmarshal(data, &v.Value)
}
func (optionalGroup) Schema(huma.Registry) *huma.Schema {
	minLength, maxLength := 1, 64
	return &huma.Schema{Type: "string", MinLength: &minLength, MaxLength: &maxLength, Pattern: groupIDPattern.String()}
}

type CreateUserInput struct {
	Body struct {
		ModelAccessGroup optionalGroup `json:"model_access_group,omitempty" doc:"Model access group. Omit to store the configured creation default."`
		ID               *string       `json:"id,omitempty" minLength:"1" maxLength:"256" doc:"Optional immutable user ID. Omit to generate one; supplied IDs are used exactly. Duplicate IDs return 409."`
		BudgetUSD        string        `json:"budget_usd" minLength:"1" maxLength:"100" doc:"Nonnegative USD, rounded to nano-USD. Zero blocks inference."`
		Cadence          string        `json:"cadence" enum:"weekly,monthly"`
		Anchor           time.Time     `json:"anchor" doc:"Renewal anchor, normalized to UTC and truncated to whole seconds. Cadence and anchor are immutable."`
	}
}
type UpdateUserInput struct {
	UserInput
	Body struct {
		ModelAccessGroup optionalGroup `json:"model_access_group,omitempty" doc:"Replaces the user group immediately for all keys. Omit to preserve the current assignment."`
		Enabled          *bool         `json:"enabled,omitempty"`
		BudgetUSD        *string       `json:"budget_usd,omitempty" minLength:"1" maxLength:"100" doc:"Applies immediately, including to the current period."`
	}
}
type CreateKeyInput struct {
	UserInput
	Body struct {
		Alias   *string    `json:"alias,omitempty" minLength:"1" maxLength:"256" doc:"Caller-supplied key alias, unique within this user. Duplicate aliases return 409, including revoked keys. Omit to generate an alias."`
		Expires *time.Time `json:"expires,omitempty" doc:"Optional future expiration."`
	}
}
type KeyAliasInput struct {
	UserInput
	Alias string `path:"key_alias" minLength:"1" maxLength:"256" doc:"Exact caller-supplied key alias from the key's alias field, unique within the identified user. No internal-ID fallback."`
}
type RotateKeyInput struct {
	KeyAliasInput
	Body *RotateKeyBody `required:"false"`
}
type RotateKeyBody struct {
	Expires *time.Time `json:"expires,omitempty" doc:"Optional future expiration of the replacement; omit for a non-expiring credential."`
}
type CreateKeyResult struct {
	Key    Key    `json:"key"`
	Secret string `json:"secret" doc:"Returned once; cannot be retrieved later."`
}
type RevokeKeyInput struct {
	Key string `path:"key" maxLength:"100"`
}
type HistoryInput struct {
	Pagination
	UserID     string `query:"user_id" minLength:"1" maxLength:"256" doc:"Filter requests by immutable user ID, caller-supplied or gateway-generated."`
	Accounting string `query:"accounting" enum:"pending,settled,unresolved"`
}
type ResolveInput struct {
	RequestInput
	Body struct {
		TotalUSD string `json:"total_usd" minLength:"1" maxLength:"100" doc:"Absolute replacement total, not an additional charge."`
		Reason   string `json:"reason" minLength:"1" maxLength:"1000"`
	}
}
type StatusResult struct {
	Status string `json:"status"`
}
type BackupResult struct {
	Filename string `json:"filename"`
}

func adminOperation(method, path, id, summary string, status int) huma.Operation {
	return huma.Operation{Method: method, Path: path, OperationID: id, Summary: summary, DefaultStatus: status, MaxBodyBytes: 1 << 20, BodyReadTimeout: 5 * time.Second, Errors: []int{400, 401, 404, 409, 422, 503}}
}

func (g *Gateway) newAdminAPI() (http.Handler, huma.API) {
	mux := http.NewServeMux()
	c := huma.DefaultConfig("LLM Gateway Admin API", "0.1.0")
	// Preserve response shapes; generated schemas do not need to add $schema to
	// every response. The generated contract is protected by the same admin auth.
	c.CreateHooks = nil
	c.RejectUnknownQueryParameters = true
	c.Info.Description = "Internal provisioning and auditable USD cost accounting. Users have a single immutable id, optionally supplied at creation or generated when omitted. User-specific routes and user_id fields use that same ID. Key routes identify keys by alias, scoped to the user. Amounts are decimal strings; *_unix timestamps are Unix seconds. Validation errors use RFC 9457 problem details."
	c.Components.SecuritySchemes = map[string]*huma.SecurityScheme{"adminBearer": {Type: "http", Scheme: "bearer"}}
	c.Security = []map[string][]string{{"adminBearer": {}}}
	api := humago.New(mux, c)
	huma.Register(api, adminOperation("POST", "/admin/v1/users", "createUser", "Create a user with an optional ID and recurring budget", 201), g.createUser)
	huma.Register(api, adminOperation("GET", "/admin/v1/users", "listUsers", "List users in ID order", 200), g.listUsers)
	huma.Register(api, adminOperation("GET", "/admin/v1/users/{user_id}", "getUser", "Get a user policy by user ID", 200), g.getUser)
	huma.Register(api, adminOperation("PATCH", "/admin/v1/users/{user_id}", "updateUser", "Enable a user or change their budget by user ID", 200), g.updateUser)
	huma.Register(api, adminOperation("GET", "/admin/v1/users/{user_id}/usage", "userUsage", "Get spending and accounting backlog by user ID", 200), g.summary)
	huma.Register(api, adminOperation("POST", "/admin/v1/users/{user_id}/keys", "createKey", "Create a key with an optional caller-supplied alias; secret is returned once", 201), g.createKey)
	listKeysOperation := adminOperation("GET", "/admin/v1/users/{user_id}/keys", "listKeys", "List current key metadata without secrets or hashes", 200)
	listKeysOperation.Description = "Returns one current credential per key alias within the user, including revoked and expired entries. Retired credential versions are preserved for accounting but omitted from this list. Creation time is preserved across rotation; rotation time records the latest rotate operation."
	huma.Register(api, listKeysOperation, g.listKeys)
	huma.Register(api, adminOperation("GET", "/admin/v1/users/{user_id}/keys/{key_alias}", "getKeyByAlias", "Get key metadata by user ID and key alias", 200), g.getKeyByAlias)
	rotateOperation := adminOperation("POST", "/admin/v1/users/{user_id}/keys/{key_alias}/rotate", "rotateKeyByAlias", "Create or atomically rotate a key's credential by alias", 200)
	rotateOperation.Description = "Creates the key for this user's alias if absent, or revokes the old credential and activates a replacement in one transaction. The new secret is returned only after commit. Retrying after a lost response rotates again. Concurrent rotations of the same alias leave only one current credential; a later rotation can supersede a secret before its caller receives it. Other aliases are unaffected. Original creation time and accounting history are preserved."
	huma.Register(api, rotateOperation, g.rotateKeyByAlias)
	revokeOperation := adminOperation("DELETE", "/admin/v1/users/{user_id}/keys/{key_alias}", "revokeKeyByAlias", "Revoke a key by user ID and key alias", 204)
	revokeOperation.Description = "Idempotent: returns 204 for already revoked or absent keys, including an absent user. Successful revocations and no-ops are audited; credential and accounting history are retained."
	huma.Register(api, revokeOperation, g.revokeKeyByAlias)
	huma.Register(api, adminOperation("DELETE", "/admin/v1/keys/{key}", "revokeKey", "Revoke a key for new requests", 204), g.revokeKey)
	huma.Register(api, adminOperation("GET", "/admin/v1/requests", "listRequests", "List request metadata newest first, optionally filtered by user ID", 200), g.history)
	huma.Register(api, adminOperation("GET", "/admin/v1/requests/{request}", "getRequest", "Get inference lifecycle and accounting state", 200), g.getRequest)
	huma.Register(api, adminOperation("GET", "/admin/v1/requests/{request}/ledger", "requestLedger", "List original charges and signed adjustments", 200), g.ledger)
	huma.Register(api, adminOperation("POST", "/admin/v1/requests/{request}/resolve", "resolveCost", "Set a finished request's absolute total and audit the adjustment", 200), g.resolve)
	huma.Register(api, adminOperation("GET", "/admin/v1/audit", "auditLog", "List administrative events in ID order", 200), g.auditLog)
	huma.Register(api, adminOperation("POST", "/admin/v1/backups", "createBackup", "Create and verify a local SQLite snapshot", 201), g.backup)
	huma.Register(api, adminOperation("GET", "/health/ready", "readiness", "Check database connectivity and admission state", 200), g.ready)
	mux.HandleFunc("GET /metrics", g.metrics)
	api.OpenAPI().AddOperation(&huma.Operation{Method: "GET", Path: "/metrics", OperationID: "metrics", Summary: "Prometheus metrics without per-user labels", Responses: map[string]*huma.Response{"200": {Description: "Prometheus exposition", Content: map[string]*huma.MediaType{"text/plain": {Schema: &huma.Schema{Type: "string"}}}}}})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { writeAdminError(w, 404, "not_found") })
	handler := g.recover(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		token := bearer(r)
		hash := sha256.Sum256([]byte(token))
		if token == "" || subtle.ConstantTimeCompare(hash[:], g.adminHash[:]) != 1 {
			writeAdminError(w, 401, "invalid_admin_credential")
			return
		}
		mux.ServeHTTP(w, r)
	}))
	return handler, api
}
func (g *Gateway) AdminHandler() http.Handler { handler, _ := g.newAdminAPI(); return handler }

// AdminOpenAPI exports the same contract served at /openapi.json on the admin listener.
// No storage or credentials are needed to generate the contract.
func AdminOpenAPI() ([]byte, error) {
	g := &Gateway{}
	_, api := g.newAdminAPI()
	return json.MarshalIndent(api.OpenAPI(), "", "  ")
}
func writeAdminError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(huma.NewError(status, code))
}
func (g *Gateway) adminError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return huma.NewError(404, "not_found")
	case errors.Is(err, ErrConflict):
		return huma.NewError(409, "conflict")
	case errors.Is(err, ErrInvalidGroup):
		return huma.NewError(400, "invalid_model_access_group")
	case errors.Is(err, ErrInvalidUserID):
		return huma.NewError(400, "invalid_user_id")
	case errors.Is(err, ErrInvalidKeyAlias):
		return huma.NewError(400, "invalid_key_alias")
	default:
		g.Log.Error("admin storage operation failed")
		return huma.NewError(503, "storage_unavailable")
	}
}

func (g *Gateway) createUser(ctx context.Context, in *CreateUserInput) (*APIOutput[User], error) {
	amount, err := ParseUSD(in.Body.BudgetUSD)
	a := in.Body.Anchor
	if err != nil || a.IsZero() || a.Year() < 1970 || a.Year() > 9990 {
		return nil, huma.NewError(400, "invalid_user_policy")
	}
	id := ""
	if in.Body.ID != nil {
		id = *in.Body.ID
		if strings.TrimSpace(id) == "" || len(id) > 256 {
			return nil, huma.NewError(400, "invalid_user_id")
		}
	}
	group := g.Config.DefaultModelAccessGroup
	if in.Body.ModelAccessGroup.Set {
		group = in.Body.ModelAccessGroup.Value
	}
	if _, ok := g.Config.ModelAccessGroups[group]; !ok {
		return nil, huma.NewError(400, "invalid_model_access_group")
	}
	u, err := g.Store.CreateUser(ctx, id, amount, in.Body.Cadence, a, group)
	if err != nil {
		return nil, g.adminError(err)
	}
	return output(u), nil
}
func (g *Gateway) listUsers(ctx context.Context, in *Pagination) (*APIOutput[Page[User]], error) {
	data, err := g.Store.Users(ctx, in.After, in.Limit)
	if err != nil {
		return nil, g.adminError(err)
	}
	return paged(data, in.Limit, func(u User) string { return u.ID }), nil
}
func (g *Gateway) getUser(ctx context.Context, in *UserInput) (*APIOutput[User], error) {
	u, err := g.Store.User(ctx, in.UserID)
	if err != nil {
		return nil, g.adminError(err)
	}
	return output(u), nil
}
func (g *Gateway) updateUser(ctx context.Context, in *UpdateUserInput) (*APIOutput[User], error) {
	var budget *int64
	if in.Body.BudgetUSD != nil {
		n, err := ParseUSD(*in.Body.BudgetUSD)
		if err != nil {
			return nil, huma.NewError(400, "invalid_budget")
		}
		budget = &n
	}
	var group *string
	if in.Body.ModelAccessGroup.Set {
		value := in.Body.ModelAccessGroup.Value
		if _, ok := g.Config.ModelAccessGroups[value]; !ok {
			return nil, huma.NewError(400, "invalid_model_access_group")
		}
		group = &value
	}
	if in.Body.Enabled == nil && budget == nil && group == nil {
		return nil, huma.NewError(400, "empty_update")
	}
	u, err := g.Store.UpdateUser(ctx, in.UserID, in.Body.Enabled, budget, group)
	if err != nil {
		return nil, g.adminError(err)
	}
	return output(u), nil
}
func (g *Gateway) summary(ctx context.Context, in *UserInput) (*APIOutput[Summary], error) {
	s, err := g.Store.Summary(ctx, in.UserID, time.Now())
	if err != nil {
		return nil, g.adminError(err)
	}
	return output(s), nil
}
func (g *Gateway) createKey(ctx context.Context, in *CreateKeyInput) (*APIOutput[CreateKeyResult], error) {
	var expires *int64
	if e := in.Body.Expires; e != nil {
		if e.Unix() <= time.Now().Unix() {
			return nil, huma.NewError(400, "invalid_expiration")
		}
		n := e.Unix()
		expires = &n
	}
	u, err := g.Store.User(ctx, in.UserID)
	if err != nil {
		return nil, g.adminError(err)
	}
	var k Key
	var secret string
	if in.Body.Alias == nil {
		k, secret, err = g.Store.CreateKey(ctx, u.ID, expires)
	} else {
		k, secret, err = g.Store.CreateKeyWithAlias(ctx, u.ID, *in.Body.Alias, expires)
	}
	if err != nil {
		return nil, g.adminError(err)
	}
	return output(CreateKeyResult{Key: k, Secret: secret}), nil
}
func (g *Gateway) listKeys(ctx context.Context, in *UserPageInput) (*APIOutput[Page[Key]], error) {
	u, err := g.Store.User(ctx, in.UserID)
	if err != nil {
		return nil, g.adminError(err)
	}
	data, err := g.Store.Keys(ctx, u.ID, in.After, in.Limit)
	if err != nil {
		return nil, g.adminError(err)
	}
	return paged(data, in.Limit, func(k Key) string { return k.ID }), nil
}
func (g *Gateway) revokeKey(ctx context.Context, in *RevokeKeyInput) (*struct{}, error) {
	if err := g.Store.RevokeKey(ctx, in.Key); err != nil {
		return nil, g.adminError(err)
	}
	return &struct{}{}, nil
}
func (g *Gateway) getKeyByAlias(ctx context.Context, in *KeyAliasInput) (*APIOutput[Key], error) {
	u, err := g.Store.User(ctx, in.UserID)
	if err != nil {
		return nil, g.adminError(err)
	}
	k, err := g.Store.KeyByAlias(ctx, u.ID, in.Alias)
	if err != nil {
		return nil, g.adminError(err)
	}
	return output(k), nil
}
func (g *Gateway) rotateKeyByAlias(ctx context.Context, in *RotateKeyInput) (*APIOutput[CreateKeyResult], error) {
	var expires *int64
	if in.Body != nil && in.Body.Expires != nil {
		e := in.Body.Expires
		if e.Unix() <= time.Now().Unix() {
			return nil, huma.NewError(400, "invalid_expiration")
		}
		n := e.Unix()
		expires = &n
	}
	u, err := g.Store.User(ctx, in.UserID)
	if err != nil {
		return nil, g.adminError(err)
	}
	k, secret, err := g.Store.RotateKey(ctx, u.ID, in.Alias, expires)
	if err != nil {
		return nil, g.adminError(err)
	}
	return output(CreateKeyResult{Key: k, Secret: secret}), nil
}
func (g *Gateway) revokeKeyByAlias(ctx context.Context, in *KeyAliasInput) (*struct{}, error) {
	if err := g.Store.RevokeKeyByAlias(ctx, in.UserID, in.Alias); err != nil {
		return nil, g.adminError(err)
	}
	return &struct{}{}, nil
}
func (g *Gateway) history(ctx context.Context, in *HistoryInput) (*APIOutput[Page[Request]], error) {
	userID := ""
	if in.UserID != "" {
		u, err := g.Store.User(ctx, in.UserID)
		if err != nil {
			return nil, g.adminError(err)
		}
		userID = u.ID
	}
	data, err := g.Store.History(ctx, userID, in.Accounting, in.After, in.Limit)
	if err != nil {
		return nil, g.adminError(err)
	}
	return paged(data, in.Limit, func(r Request) string { return r.ID }), nil
}
func (g *Gateway) getRequest(ctx context.Context, in *RequestInput) (*APIOutput[Request], error) {
	data, err := g.Store.Request(ctx, in.Request)
	if err != nil {
		return nil, g.adminError(err)
	}
	return output(data), nil
}
func (g *Gateway) resolve(ctx context.Context, in *ResolveInput) (*APIOutput[Request], error) {
	n, err := ParseUSD(in.Body.TotalUSD)
	if err != nil || strings.TrimSpace(in.Body.Reason) == "" || len(in.Body.Reason) > 1000 {
		return nil, huma.NewError(400, "invalid_resolution")
	}
	if err = g.Store.Settle(ctx, in.Request, n, in.Body.TotalUSD, nil, "", "admin: "+in.Body.Reason, true); err != nil {
		return nil, g.adminError(err)
	}
	return g.getRequest(ctx, &in.RequestInput)
}
func numericCursor(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, huma.NewError(400, "invalid_cursor")
	}
	return n, nil
}
func (g *Gateway) ledger(ctx context.Context, in *RequestPageInput) (*APIOutput[Page[LedgerEntry]], error) {
	cursor, err := numericCursor(in.After)
	if err != nil {
		return nil, err
	}
	data, err := g.Store.Ledger(ctx, in.Request, cursor, in.Limit)
	if err != nil {
		return nil, g.adminError(err)
	}
	return paged(data, in.Limit, func(e LedgerEntry) string { return strconv.FormatInt(e.ID, 10) }), nil
}
func (g *Gateway) auditLog(ctx context.Context, in *Pagination) (*APIOutput[Page[AuditEntry]], error) {
	cursor, err := numericCursor(in.After)
	if err != nil {
		return nil, err
	}
	data, err := g.Store.AuditLog(ctx, cursor, in.Limit)
	if err != nil {
		return nil, g.adminError(err)
	}
	return paged(data, in.Limit, func(e AuditEntry) string { return strconv.FormatInt(e.ID, 10) }), nil
}
func (g *Gateway) backup(ctx context.Context, in *struct{}) (*APIOutput[BackupResult], error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	name, err := g.CreateBackup(ctx)
	if err != nil {
		g.Log.Error("backup failed")
		return nil, huma.NewError(503, "backup_failed")
	}
	return output(BackupResult{Filename: name}), nil
}
func (g *Gateway) ready(ctx context.Context, in *struct{}) (*APIOutput[StatusResult], error) {
	if g.closing.Load() {
		return nil, huma.NewError(503, "shutting_down")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := g.Store.ValidateUserGroups(ctx, g.Config); err != nil {
		return nil, g.adminError(err)
	}
	return output(StatusResult{Status: "ready"}), nil
}

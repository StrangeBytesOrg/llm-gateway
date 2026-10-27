package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func keyAliasPath(user, key string) string {
	return "/admin/v1/users/" + url.PathEscape(user) + "/keys/" + url.PathEscape(key)
}

func rotateAdminKey(t *testing.T, h http.Handler, admin, path, body string) CreateKeyResult {
	t.Helper()
	w := call(h, "POST", path+"/rotate", admin, body)
	if w.Code != 200 {
		t.Fatalf("rotate: %d %s", w.Code, w.Body)
	}
	var result CreateKeyResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Secret == "" || result.Key.Status != "active" || result.Key.Rotated == nil {
		t.Fatalf("rotation metadata/secret missing: %+v", result.Key)
	}
	return result
}

func assertKeyMetadata(t *testing.T, w *httptest.ResponseRecorder, secret string) Key {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("key metadata: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), `"secret"`) || strings.Contains(w.Body.String(), `"hash"`) || strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), keyHash(secret)) {
		t.Fatal("metadata leaked a secret or hash")
	}
	var k Key
	if err := json.Unmarshal(w.Body.Bytes(), &k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestAdminKeyAliasWorkflow(t *testing.T) {
	g, _, admin := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"gen-alias","choices":[],"usage":{"cost":0.1}}`)
	})
	h := g.AdminHandler()
	u := createAdminUser(t, h, admin, "hub-aliases")
	path := keyAliasPath(u.ID, "alias-42")
	if w := call(h, "GET", path, admin, ""); w.Code != 404 {
		t.Fatalf("missing key: %d %s", w.Code, w.Body)
	}
	// A plain curl -X POST needs neither a body nor Content-Type to connect.
	r := httptest.NewRequest("POST", path+"/rotate", nil)
	r.Header.Set("Authorization", "Bearer "+admin)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("create-on-rotate without body: %d %s", w.Code, w.Body)
	}
	var first CreateKeyResult
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Key.Alias != "alias-42" || first.Key.UserID != u.ID || first.Key.Created <= 0 || first.Key.Rotated == nil || first.Key.Status != "active" || first.Secret == "" {
		t.Fatalf("created metadata: %+v", first.Key)
	}
	// Requests admitted under the old credential must settle after rotation.
	request, err := g.Store.Admit(context.Background(), first.Secret, "alias", testRoute(), g.Config.Providers["upstream"], time.Now(), testAccessConfig())
	if err != nil {
		t.Fatal(err)
	}
	second := rotateAdminKey(t, h, admin, path, `{}`)
	if second.Secret == first.Secret || second.Key.ID == first.Key.ID || second.Key.Created != first.Key.Created {
		t.Fatal("retry did not replace the credential while preserving creation time")
	}
	assertCredential(t, g.Store, first.Secret, false)
	assertCredential(t, g.Store, second.Secret, true)
	metadata := assertKeyMetadata(t, call(h, "GET", path, admin, ""), second.Secret)
	if metadata.ID != second.Key.ID || metadata.Alias != first.Key.Alias || metadata.Rotated == nil {
		t.Fatalf("GET metadata: %+v", metadata)
	}
	w = call(h, "GET", "/admin/v1/users/"+u.ID+"/keys", admin, "")
	var keys Page[Key]
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &keys) != nil || len(keys.Data) != 1 || keys.Data[0].ID != second.Key.ID || keys.Data[0].Status != "active" || keys.Data[0].Rotated == nil {
		t.Fatalf("list metadata: %d %s", w.Code, w.Body)
	}
	for _, value := range []string{first.Secret, second.Secret, keyHash(first.Secret), keyHash(second.Secret), `"secret"`, `"hash"`} {
		if strings.Contains(w.Body.String(), value) {
			t.Fatal("key list exposed a credential")
		}
	}
	for _, tc := range []struct{ method, suffix string }{{"GET", ""}, {"POST", "/rotate"}, {"DELETE", ""}} {
		if w := call(h, tc.method, path+tc.suffix, second.Secret, ""); w.Code != 401 {
			t.Fatalf("virtual key accessed admin %s: %d", tc.method, w.Code)
		}
	}
	assertCredential(t, g.Store, second.Secret, true)
	for range 2 {
		w = call(h, "DELETE", path, admin, "")
		if w.Code != 204 || w.Body.Len() != 0 {
			t.Fatalf("idempotent revoke: %d %s", w.Code, w.Body)
		}
	}
	assertCredential(t, g.Store, second.Secret, false)
	metadata = assertKeyMetadata(t, call(h, "GET", path, admin, ""), second.Secret)
	if metadata.Status != "revoked" || !metadata.Revoked {
		t.Fatalf("revoked metadata: %+v", metadata)
	}
	w = call(h, "GET", "/admin/v1/users/"+u.ID+"/keys", admin, "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &keys) != nil || len(keys.Data) != 1 || keys.Data[0].Status != "revoked" {
		t.Fatalf("revoked list metadata: %d %s", w.Code, w.Body)
	}
	for _, missing := range []string{keyAliasPath(u.ID, "absent"), keyAliasPath("missing-user", "alias-42")} {
		if w := call(h, "DELETE", missing, admin, ""); w.Code != 204 {
			t.Fatalf("missing revoke: %d %s", w.Code, w.Body)
		}
	}
	third := rotateAdminKey(t, h, admin, path, "")
	assertCredential(t, g.Store, third.Secret, true)
	if third.Key.Created != first.Key.Created {
		t.Fatal("revocation lost original creation timestamp")
	}
	if err = g.Store.Finish(context.Background(), request.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if err = g.Store.Settle(context.Background(), request.ID, 100_000_000, "0.1", nil, "provider_usage", "", false); err != nil {
		t.Fatal(err)
	}
	w = call(h, "GET", "/admin/v1/requests/"+request.ID, admin, "")
	var history Request
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &history) != nil || history.KeyID != first.Key.ID || history.Accounting != "settled" {
		t.Fatalf("retired credential accounting: %d %s", w.Code, w.Body)
	}
	events, err := g.Store.AuditLog(context.Background(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	rotations, revocations := 0, 0
	for _, e := range events {
		for _, secret := range []string{first.Secret, second.Secret, third.Secret, keyHash(first.Secret), keyHash(second.Secret), keyHash(third.Secret)} {
			if strings.Contains(string(e.Detail), secret) {
				t.Fatal("audit leaked a secret or hash")
			}
		}
		var detail struct {
			Alias  string `json:"alias"`
			UserID string `json:"user_id"`
		}
		if err = json.Unmarshal(e.Detail, &detail); err != nil {
			t.Fatal(err)
		}
		if detail.Alias == first.Key.Alias && detail.UserID == u.ID {
			switch e.Action {
			case "key.rotate":
				rotations++
			case "key.revoke":
				revocations++
			}
		}
	}
	if rotations != 3 || revocations != 2 {
		t.Fatalf("rotation/revocation audit counts: %d/%d", rotations, revocations)
	}
}

func TestAdminKeyAliasScopeAndIdentifiers(t *testing.T) {
	g, _, admin := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected inference") })
	h := g.AdminHandler()
	a := createAdminUser(t, h, admin, "hub-key-owner")
	b := createAdminUser(t, h, admin, "hub-key-owner-b")
	pa := keyAliasPath(a.ID, "same-alias")
	pb := keyAliasPath(b.ID, "same-alias")
	ka := rotateAdminKey(t, h, admin, pa, "")
	kb := rotateAdminKey(t, h, admin, pb, "")
	if ka.Key.UserID != a.ID || kb.Key.UserID != b.ID {
		t.Fatal("key belongs to the wrong user")
	}
	if w := call(h, "DELETE", pb, admin, ""); w.Code != 204 {
		t.Fatal(w.Body)
	}
	assertCredential(t, g.Store, ka.Secret, true)
	assertCredential(t, g.Store, kb.Secret, false)
	// A generated credential ID is never a fallback for the alias.
	if w := call(h, "GET", keyAliasPath(a.ID, ka.Key.ID), admin, ""); w.Code != 404 {
		t.Fatalf("internal key ID fallback: %d %s", w.Code, w.Body)
	}
	// That same string may legitimately name another key; exact matching wins.
	pk := keyAliasPath(a.ID, ka.Key.ID)
	collision := rotateAdminKey(t, h, admin, pk, "")
	k := assertKeyMetadata(t, call(h, "GET", pk, admin, ""), collision.Secret)
	if k.ID != collision.Key.ID || k.Alias != ka.Key.ID {
		t.Fatal("key ID collision resolved incorrectly")
	}
	if w := call(h, "DELETE", pk, admin, ""); w.Code != 204 {
		t.Fatal(w.Body)
	}
	assertCredential(t, g.Store, ka.Secret, true)
	assertCredential(t, g.Store, collision.Secret, false)
	for _, name := range []string{"alias/member +?%#", strings.Repeat("i", 256), "别名-ID"} {
		p := keyAliasPath(a.ID, name)
		created := rotateAdminKey(t, h, admin, p, "")
		k := assertKeyMetadata(t, call(h, "GET", p, admin, ""), created.Secret)
		if k.Alias != name {
			t.Fatalf("key alias did not round trip: %q", k.Alias)
		}
	}
}

func TestAdminKeyAliasCreationExpiryAndFailures(t *testing.T) {
	g, _, admin := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected inference") })
	h := g.AdminHandler()
	u := createAdminUser(t, h, admin, "hub-key-validation")
	base := "/admin/v1/users/" + u.ID + "/keys"
	path := keyAliasPath(u.ID, "manual-alias")
	w := call(h, "POST", base, admin, `{"alias":"manual-alias"}`)
	var initial CreateKeyResult
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &initial) != nil || initial.Key.Alias != "manual-alias" || initial.Key.Rotated != nil {
		t.Fatalf("named creation: %d %s", w.Code, w.Body)
	}
	if w := call(h, "POST", base, admin, `{"alias":"manual-alias"}`); w.Code != 409 {
		t.Fatalf("duplicate key alias: %d %s", w.Code, w.Body)
	}
	future := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	body, err := json.Marshal(map[string]any{"expires": future})
	if err != nil {
		t.Fatal(err)
	}
	active := rotateAdminKey(t, h, admin, path, string(body))
	if active.Key.Expires == nil || *active.Key.Expires != future.Unix() {
		t.Fatalf("expiration not applied: %+v", active.Key)
	}
	if _, err = g.Store.db.Exec("UPDATE virtual_keys SET expires=? WHERE id=?", time.Now().Add(-time.Second).Unix(), active.Key.ID); err != nil {
		t.Fatal(err)
	}
	k := assertKeyMetadata(t, call(h, "GET", path, admin, ""), active.Secret)
	if k.Status != "expired" || k.Revoked {
		t.Fatalf("expired metadata: %+v", k)
	}
	assertCredential(t, g.Store, active.Secret, false)
	current := rotateAdminKey(t, h, admin, path, "")
	if current.Key.Expires != nil {
		t.Fatal("default rotation retained an expired deadline")
	}
	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{base, `{"alias":""}`, 422},
		{base, `{"alias":"   "}`, 400},
		{path + "/rotate", `{"expires":"2020-01-01T00:00:00Z"}`, 400},
		{path + "/rotate", `{"alias":"cannot-override-path"}`, 422},
		{keyAliasPath(u.ID, strings.Repeat("x", 257)) + "/rotate", `{}`, 422},
		{keyAliasPath("unknown-user", "key") + "/rotate", `{}`, 404},
	} {
		w := call(h, "POST", tc.path, admin, tc.body)
		if w.Code != tc.status {
			t.Errorf("%s: %d %s", tc.path, w.Code, w.Body)
		}
	}
	assertCredential(t, g.Store, current.Secret, true)
	if _, err = g.Store.db.Exec(`CREATE TRIGGER fail_admin_rotation BEFORE INSERT ON audit WHEN NEW.action='key.rotate' BEGIN SELECT RAISE(ABORT,'injected rotation failure'); END`); err != nil {
		t.Fatal(err)
	}
	w = call(h, "POST", path+"/rotate", admin, "")
	if w.Code != 503 || strings.Contains(w.Body.String(), `"secret"`) || strings.Contains(w.Body.String(), "injected rotation failure") {
		t.Fatalf("failed rotation response: %d %s", w.Code, w.Body)
	}
	assertCredential(t, g.Store, current.Secret, true)
	k = assertKeyMetadata(t, call(h, "GET", path, admin, ""), current.Secret)
	if k.ID != current.Key.ID || k.Status != "active" {
		t.Fatal("failed HTTP rotation changed current key")
	}
}

func TestKeyAliasOpenAPI(t *testing.T) {
	b, err := AdminOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name   string `json:"name"`
				In     string `json:"in"`
				Schema struct {
					MaxLength int `json:"maxLength"`
				} `json:"schema"`
			} `json:"parameters"`
			RequestBody *struct {
				Required bool `json:"required"`
			} `json:"requestBody"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err = json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, suffix string }{{"get", ""}, {"delete", ""}, {"post", "/rotate"}} {
		path := "/admin/v1/users/{user_id}/keys/{key_alias}" + tc.suffix
		op, ok := spec.Paths[path][tc.method]
		if !ok {
			t.Fatalf("missing %s %s", tc.method, path)
		}
		found := map[string]bool{}
		for _, p := range op.Parameters {
			if p.In == "path" {
				found[p.Name] = true
				if p.Schema.MaxLength != 256 {
					t.Errorf("invalid path length: %+v", p)
				}
			}
		}
		if !found["user_id"] || !found["key_alias"] || len(found) != 2 {
			t.Fatalf("path identifiers: %+v", found)
		}
		if tc.suffix == "/rotate" && op.RequestBody != nil && op.RequestBody.Required {
			t.Fatal("rotate incorrectly requires a body")
		}
	}
	fields := spec.Components.Schemas["Key"].Properties
	for _, name := range []string{"alias", "status", "created_unix", "rotated_unix"} {
		if _, ok := fields[name]; !ok {
			t.Errorf("key metadata schema missing %s", name)
		}
	}
	for _, name := range []string{"secret", "hash", "is_current"} {
		if _, ok := fields[name]; ok {
			t.Errorf("key metadata schema exposes %s", name)
		}
	}
	if _, ok := spec.Components.Schemas["CreateKeyInputBody"].Properties["alias"]; !ok {
		t.Error("key creation lacks an alias input")
	}
}

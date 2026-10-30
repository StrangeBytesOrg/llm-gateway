package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func createAdminUser(t *testing.T, h http.Handler, admin, userID string) User {
	t.Helper()
	body, err := json.Marshal(map[string]any{"id": userID, "budget_usd": "10", "cadence": "monthly", "anchor": "2025-01-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	w := call(h, "POST", "/admin/v1/users", admin, string(body))
	if w.Code != 201 {
		t.Fatalf("create user: %d %s", w.Code, w.Body)
	}
	var u User
	if err := json.Unmarshal(w.Body.Bytes(), &u); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestAdminUserIDRecoveryAndWorkflow(t *testing.T) {
	g, otherKey, admin := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"gen-user-id","choices":[],"usage":{"cost":0.1}}`)
	})
	h := g.AdminHandler()
	const userID = "hub-user-42"
	const creation = `{"id":"hub-user-42","budget_usd":"10","cadence":"monthly","anchor":"2025-01-01T00:00:00Z"}`
	// Discard the creation response: the caller only retains its own user ID.
	if w := call(h, "POST", "/admin/v1/users", admin, creation); w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	path := "/admin/v1/users/" + userID
	w := call(h, "GET", path, admin, "")
	if w.Code != 200 {
		t.Fatalf("recover by user ID: %d %s", w.Code, w.Body)
	}
	var u User
	if err := json.Unmarshal(w.Body.Bytes(), &u); err != nil {
		t.Fatal(err)
	}
	if u.ID != userID {
		t.Fatalf("user identity: %+v", u)
	}
	if w := call(h, "POST", "/admin/v1/users", admin, creation); w.Code != 409 {
		t.Fatalf("duplicate creation: %d %s", w.Code, w.Body)
	}
	var count int
	if err := g.Store.db.QueryRow("SELECT count(*) FROM users WHERE id=?", userID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate created another user: count=%d error=%v", count, err)
	}

	// Unknown IDs must not modify the existing user or create keys.
	for _, tc := range []struct{ method, suffix, body string }{
		{"GET", "", ""}, {"PATCH", "", `{"enabled":false}`},
		{"GET", "/usage", ""}, {"POST", "/keys", `{}`}, {"GET", "/keys", ""},
	} {
		if w := call(h, tc.method, "/admin/v1/users/missing-user"+tc.suffix, admin, tc.body); w.Code != 404 {
			t.Fatalf("unknown user: %d %s", w.Code, w.Body)
		}
	}
	w = call(h, "POST", path+"/keys", admin, `{}`)
	if w.Code != 201 {
		t.Fatalf("create key by user ID: %d %s", w.Code, w.Body)
	}
	var key CreateKeyResult
	if err := json.Unmarshal(w.Body.Bytes(), &key); err != nil {
		t.Fatal(err)
	}
	principal, err := g.Store.Authenticate(context.Background(), key.Secret)
	if err != nil || principal.UserID != u.ID || key.Key.UserID != u.ID {
		t.Fatalf("key lost user relationship: %+v %+v %v", principal, key.Key, err)
	}
	w = call(h, "GET", path+"/keys", admin, "")
	var listed Page[Key]
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &listed) != nil || len(listed.Data) != 1 || listed.Data[0].ID != key.Key.ID {
		t.Fatalf("list keys by user ID: %d %s", w.Code, w.Body)
	}
	// Two users incur charges; the user-ID history filter must isolate one.
	for _, secret := range []string{key.Secret, otherKey} {
		w := call(g.InferenceHandler(), "POST", "/v1/chat/completions", secret, `{"model":"alias","messages":[{}]}`)
		if w.Code != 200 {
			t.Fatalf("inference: %d %s", w.Code, w.Body)
		}
	}
	w = call(h, "GET", path+"/usage", admin, "")
	var usage Summary
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &usage) != nil || usage.User.ID != u.ID || usage.Current == nil || usage.Current.SpentUSD != "0.100000000" {
		t.Fatalf("usage by user ID: %d %s", w.Code, w.Body)
	}
	w = call(h, "GET", "/admin/v1/requests?user_id="+url.QueryEscape(userID), admin, "")
	var history Page[Request]
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &history) != nil || len(history.Data) != 1 || history.Data[0].UserID != u.ID {
		t.Fatalf("history by user ID: %d %s", w.Code, w.Body)
	}
	w = call(h, "PATCH", path, admin, `{"enabled":false}`)
	var updated User
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &updated) != nil || updated.ID != u.ID || updated.Enabled || updated.BudgetUSD != "10.000000000" {
		t.Fatalf("update by user ID: %d %s", w.Code, w.Body)
	}
}

func TestAdminGeneratedUserID(t *testing.T) {
	g, _, admin := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected inference") })
	h := g.AdminHandler()
	const body = `{"budget_usd":"10","cadence":"monthly","anchor":"2025-01-01T00:00:00Z"}`
	var ids []string
	for range 2 {
		w := call(h, "POST", "/admin/v1/users", admin, body)
		var u User
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &u) != nil || !strings.HasPrefix(u.ID, "usr_") {
			t.Fatalf("generate ID: %d %s", w.Code, w.Body)
		}
		ids = append(ids, u.ID)
		path := "/admin/v1/users/" + u.ID
		if w := call(h, "GET", path, admin, ""); w.Code != 200 {
			t.Fatal(w.Body)
		}
		if w := call(h, "PATCH", path, admin, `{"enabled":true}`); w.Code != 200 {
			t.Fatal(w.Body)
		}
		if w := call(h, "GET", path+"/usage", admin, ""); w.Code != 200 {
			t.Fatal(w.Body)
		}
		key := rotateAdminKey(t, h, admin, keyAliasPath(u.ID, "generated-owner"), "")
		principal, err := g.Store.Authenticate(context.Background(), key.Secret)
		if err != nil || principal.UserID != u.ID || key.Key.UserID != u.ID {
			t.Fatalf("generated ID relationships: %+v %v", principal, err)
		}
		duplicate := `{"id":"` + u.ID + `","budget_usd":"10","cadence":"monthly","anchor":"2025-01-01T00:00:00Z"}`
		if w := call(h, "POST", "/admin/v1/users", admin, duplicate); w.Code != 409 {
			t.Fatalf("generated ID collision: %d %s", w.Code, w.Body)
		}
	}
	if ids[0] == ids[1] {
		t.Fatal("generated duplicate IDs")
	}
}

func TestAdminUserIDValidationAndImmutability(t *testing.T) {
	g, _, admin := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected inference") })
	h := g.AdminHandler()
	for _, id := range []string{"", "   ", strings.Repeat("x", 257)} {
		body, _ := json.Marshal(map[string]any{"id": id, "budget_usd": "10", "cadence": "monthly", "anchor": "2025-01-01T00:00:00Z"})
		if w := call(h, "POST", "/admin/v1/users", admin, string(body)); w.Code != 400 && w.Code != 422 {
			t.Fatalf("invalid ID %q: %d %s", id, w.Code, w.Body)
		}
	}
	u := createAdminUser(t, h, admin, "immutable-id")
	if w := call(h, "PATCH", "/admin/v1/users/"+u.ID, admin, `{"id":"replacement","enabled":false}`); w.Code != 422 {
		t.Fatalf("ID update accepted: %d %s", w.Code, w.Body)
	}
	stored, err := g.Store.User(context.Background(), u.ID)
	if err != nil || !stored.Enabled {
		t.Fatalf("rejected update changed user: %+v %v", stored, err)
	}
}

func TestAdminUserIDPathValues(t *testing.T) {
	g, _, admin := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected inference") })
	h := g.AdminHandler()
	for _, userID := range []string{strings.Repeat("x", 256), "tenant/member +?%#", "hub-用户", "HubCase"} {
		t.Run(userID, func(t *testing.T) {
			u := createAdminUser(t, h, admin, userID)
			path := "/admin/v1/users/" + url.PathEscape(userID)
			w := call(h, "GET", path, admin, "")
			var got User
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.ID != u.ID || got.ID != userID {
				t.Fatalf("user ID did not round trip: %d %s", w.Code, w.Body)
			}
			w = call(h, "POST", path+"/keys", admin, `{}`)
			if w.Code != 201 {
				t.Fatalf("key path: %d %s", w.Code, w.Body)
			}
		})
	}
	if w := call(h, "GET", "/admin/v1/users/hubcase", admin, ""); w.Code != 404 {
		t.Fatalf("user ID was matched case-insensitively: %d %s", w.Code, w.Body)
	}
}

func TestAdminUserIDOpenAPI(t *testing.T) {
	data, err := AdminOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			} `json:"schemas"`
		} `json:"components"`
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name        string `json:"name"`
				In          string `json:"in"`
				Description string `json:"description"`
				Schema      struct {
					MaxLength int `json:"maxLength"`
				} `json:"schema"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"User", "CreateUserInputBody"} {
		schema, ok := spec.Components.Schemas[name]
		if !ok {
			t.Fatalf("missing schema %s", name)
		}
		if _, ok := schema.Properties["id"]; !ok {
			t.Errorf("%s lacks id", name)
		}
		if name == "CreateUserInputBody" {
			for _, field := range schema.Required {
				if field == "id" {
					t.Error("creation requires id")
				}
			}
		}
	}
	for _, tc := range []struct{ path, method string }{
		{"/admin/v1/users/{user_id}", "get"},
		{"/admin/v1/users/{user_id}", "patch"},
		{"/admin/v1/users/{user_id}/usage", "get"},
		{"/admin/v1/users/{user_id}/keys", "post"},
		{"/admin/v1/users/{user_id}/keys", "get"},
	} {
		operation, ok := spec.Paths[tc.path][tc.method]
		if !ok {
			t.Errorf("missing operation %s %s", tc.method, tc.path)
			continue
		}
		found := false
		for _, p := range operation.Parameters {
			if p.In == "path" {
				found = true
				if p.Name != "user_id" || p.Schema.MaxLength != 256 || !strings.Contains(p.Description, "user ID") {
					t.Errorf("incorrect user-ID parameter: %+v", p)
				}
			}
		}
		if !found {
			t.Errorf("missing path parameter for %s %s", tc.method, tc.path)
		}
	}
	found := false
	for _, p := range spec.Paths["/admin/v1/requests"]["get"].Parameters {
		if p.Name == "user_id" && p.In == "query" {
			found = true
			if p.Schema.MaxLength != 256 || !strings.Contains(p.Description, "user ID") {
				t.Errorf("incorrect user-ID history filter: %+v", p)
			}
		}
	}
	if !found {
		t.Error("history is missing its user-ID filter")
	}
}

func TestAdminUserPaginationWithLongIDs(t *testing.T) {
	g, _, admin := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected inference") })
	h := g.AdminHandler()
	first := strings.Repeat("a", 256)
	second := strings.Repeat("b", 256)
	createAdminUser(t, h, admin, first)
	createAdminUser(t, h, admin, second)
	w := call(h, "GET", "/admin/v1/users?limit=1", admin, "")
	var page Page[User]
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Data) != 1 || page.Data[0].ID != first || page.NextCursor != first {
		t.Fatalf("first page: %d %s", w.Code, w.Body)
	}
	w = call(h, "GET", "/admin/v1/users?limit=1&after="+url.QueryEscape(page.NextCursor), admin, "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Data) != 1 || page.Data[0].ID != second {
		t.Fatalf("long ID cursor: %d %s", w.Code, w.Body)
	}
}

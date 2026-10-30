package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAdminGeneratedContract(t *testing.T) {
	generated, err := AdminOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		OpenAPI string                     `json:"openapi"`
		Paths   map[string]json.RawMessage `json:"paths"`
	}
	if err = json.Unmarshal(generated, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.OpenAPI != "3.1.0" || len(spec.Paths) != 17 {
		t.Fatalf("unexpected contract: %s paths=%d", spec.OpenAPI, len(spec.Paths))
	}
	if _, exists := spec.Paths["/v1/chat/completions"]; exists {
		t.Fatal("inference handler was registered through Huma")
	}
	if bytes.Contains(generated, []byte(`"hash"`)) || bytes.Contains(generated, []byte(`"route"`)) {
		t.Fatal("internal persistence fields leaked into admin contract")
	}
}

func TestHumaValidationAndAdminWorkflow(t *testing.T) {
	g, key, admin := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("admin request reached upstream") })
	h := g.AdminHandler()
	for _, path := range []string{"/docs", "/schemas/User.json", "/admin/v1/users", "/metrics", "/health/ready", "/openapi.json/", "/openapi.yaml/"} {
		for _, token := range []string{"", key} {
			if w := call(h, "GET", path, token, ""); w.Code != 401 {
				t.Fatalf("unprotected admin route %s: %d", path, w.Code)
			}
		}
	}
	for _, path := range []string{"/openapi.json", "/openapi.yaml"} {
		for _, token := range []string{"", key, admin} {
			if w := call(h, "GET", path, token, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "createUser") {
				t.Fatalf("contract %s: %d %s", path, w.Code, w.Body)
			}
			if w := call(h, "HEAD", path, token, ""); w.Code != 200 {
				t.Fatalf("contract HEAD %s: %d", path, w.Code)
			}
		}
		if w := call(h, "POST", path, "", ""); w.Code != 401 {
			t.Fatalf("unprotected contract POST %s: %d", path, w.Code)
		}
	}
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/admin/v1/users?limit=0", ""},
		{"GET", "/admin/v1/users?limit=501", ""},
		{"GET", "/admin/v1/users?typo=1", ""},
		{"POST", "/admin/v1/users", `{"id":"missing required fields"}`},
		{"POST", "/admin/v1/users", `{"id":"x","budget_usd":"1","cadence":"daily","anchor":"2025-01-01T00:00:00Z"}`},
		{"POST", "/admin/v1/users", `{"id":"x","budget_usd":"1","cadence":"weekly","anchor":"2025-01-01T00:00:00Z","typo":true}`},
	} {
		w := call(h, tc.method, tc.path, admin, tc.body)
		if w.Code != 422 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/problem+json") {
			t.Errorf("validation %s: %d %s", tc.path, w.Code, w.Body)
		}
	}
	w := call(h, "POST", "/admin/v1/users", admin, `{"id":"workflow","budget_usd":"2.50","cadence":"weekly","anchor":"2025-01-01T00:00:00.123Z"}`)
	if w.Code != 201 {
		t.Fatalf("create user %d: %s", w.Code, w.Body)
	}
	var u User
	if err := json.Unmarshal(w.Body.Bytes(), &u); err != nil {
		t.Fatal(err)
	}
	if u.Anchor != 1735689600 {
		t.Fatalf("anchor was not truncated to whole seconds: %d", u.Anchor)
	}
	w = call(h, "PATCH", "/admin/v1/users/"+u.ID, admin, `{"enabled":false}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	w = call(h, "POST", "/admin/v1/users/"+u.ID+"/keys", admin, `{}`)
	if w.Code != 201 {
		t.Fatalf("create key: %d %s", w.Code, w.Body)
	}
	var result CreateKeyResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	w = call(h, "GET", "/admin/v1/users/"+u.ID+"/keys", admin, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), result.Secret) {
		t.Fatal("listed key secret")
	}
	w = call(h, "DELETE", "/admin/v1/keys/"+result.Key.ID, admin, "")
	if w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("revoke: %d %s", w.Code, w.Body)
	}
	w = call(h, "GET", "/admin/v1/users/missing", admin, "")
	if w.Code != 404 || !strings.Contains(w.Body.String(), `"detail":"not_found"`) {
		t.Fatalf("error: %d %s", w.Code, w.Body)
	}
	w = call(h, "POST", "/admin/v1/backups", admin, "")
	if w.Code != 201 || !strings.Contains(w.Body.String(), "filename") {
		t.Fatalf("backup: %d %s", w.Code, w.Body)
	}
}

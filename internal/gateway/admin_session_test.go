package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAdminSessionAuthentication(t *testing.T) {
	g, _, admin := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("admin request reached upstream") })
	h := g.AdminHandler()

	w := call(h, "POST", "/admin/v1/session", "", `{"username":"admin","password":"wrong password"}`)
	if w.Code != 401 || !strings.Contains(w.Body.String(), "invalid_admin_credential") {
		t.Fatalf("bad login: %d %s", w.Code, w.Body)
	}
	w = call(h, "POST", "/admin/v1/session", "", `{"username":"admin","password":"correct horse battery staple"}`)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	var login CreateSessionResult
	if err := json.Unmarshal(w.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	if login.Token == "" || login.Username != "admin" || login.Expires == 0 {
		t.Fatalf("login result: %+v", login)
	}

	w = call(h, "GET", "/admin/v1/users", login.Token, "")
	if w.Code != 200 {
		t.Fatalf("session-authenticated request: %d %s", w.Code, w.Body)
	}
	w = call(h, "GET", "/admin/v1/session", login.Token, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"kind":"session"`) {
		t.Fatalf("session info: %d %s", w.Code, w.Body)
	}
	w = call(h, "GET", "/admin/v1/session", admin, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"kind":"token"`) {
		t.Fatalf("token info: %d %s", w.Code, w.Body)
	}
	w = call(h, "GET", "/admin/v1/session", "", "")
	if w.Code != 401 {
		t.Fatalf("unauthenticated session info: %d %s", w.Code, w.Body)
	}

	w = call(h, "GET", "/admin/v1/config", login.Token, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"free"`) || !strings.Contains(w.Body.String(), `"admin"`) {
		t.Fatalf("config: %d %s", w.Code, w.Body)
	}
	w = call(h, "GET", "/admin/v1/overview", login.Token, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"users"`) {
		t.Fatalf("overview: %d %s", w.Code, w.Body)
	}
	w = call(h, "GET", "/admin/v1/audit?order=desc&limit=2", login.Token, "")
	if w.Code != 200 {
		t.Fatalf("audit desc: %d %s", w.Code, w.Body)
	}
	var audit Page[AuditEntry]
	if err := json.Unmarshal(w.Body.Bytes(), &audit); err != nil {
		t.Fatal(err)
	}
	if len(audit.Data) != 2 || audit.Data[0].ID <= audit.Data[1].ID {
		t.Fatalf("audit was not newest first: %+v", audit.Data)
	}

	w = call(h, "DELETE", "/admin/v1/session", login.Token, "")
	if w.Code != 204 {
		t.Fatalf("logout: %d %s", w.Code, w.Body)
	}
	w = call(h, "GET", "/admin/v1/users", login.Token, "")
	if w.Code != 401 {
		t.Fatalf("session survived logout: %d %s", w.Code, w.Body)
	}
	w = call(h, "GET", "/admin/v1/users", admin, "")
	if w.Code != 200 {
		t.Fatalf("admin token stopped working: %d %s", w.Code, w.Body)
	}

	w = call(h, "GET", "/", "", "")
	if w.Code == 401 {
		t.Fatal("admin UI root required authentication")
	}
}

func TestAdminSessionDisabledWithoutCredentials(t *testing.T) {
	g, _, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("admin request reached upstream") })
	g.adminUsername = ""
	h := g.AdminHandler()
	w := call(h, "POST", "/admin/v1/session", "", `{"username":"admin","password":"correct horse battery staple"}`)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "admin_ui_not_configured") {
		t.Fatalf("disabled login: %d %s", w.Code, w.Body)
	}
}

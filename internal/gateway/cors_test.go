package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInferenceCORS(t *testing.T) {
	g, key, admin := fixture(t, func(http.ResponseWriter, *http.Request) {})
	h := g.InferenceHandler()
	for _, path := range []string{"/v1/models", "/v1/chat/completions"} {
		r := httptest.NewRequest(http.MethodOptions, path, nil)
		r.Header.Set("Origin", "https://app.example")
		r.Header.Set("Access-Control-Request-Method", "POST")
		r.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "*" || !strings.Contains(w.Header().Get("Access-Control-Allow-Methods"), "POST") || w.Header().Get("Access-Control-Allow-Headers") != "authorization, content-type" {
			t.Fatalf("preflight %s: status %d headers %v", path, w.Code, w.Header())
		}
	}
	for _, tc := range []struct {
		key    string
		status int
	}{{key, 200}, {"invalid", 401}} {
		w := call(h, "GET", "/v1/models", tc.key, "")
		if w.Code != tc.status || w.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("models: status %d headers %v", w.Code, w.Header())
		}
	}
	w := call(g.AdminHandler(), "GET", "/admin/v1/users", admin, "")
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("admin listener unexpectedly allows CORS")
	}
}

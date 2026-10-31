package gateway

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

func TestAdminUIServesSPA(t *testing.T) {
	g, _, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("admin request reached upstream") })
	h := g.AdminHandler()

	index := call(h, "GET", "/", "", "")
	if index.Code == http.StatusServiceUnavailable {
		t.Skip("admin UI was not built before this test run")
	}
	if index.Code != http.StatusOK || !strings.Contains(index.Body.String(), `id="app"`) {
		t.Fatalf("UI index: %d %s", index.Code, index.Body)
	}
	if !strings.Contains(index.Header().Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatal("UI index is missing its content security policy")
	}

	route := call(h, "GET", "/users/example", "", "")
	if route.Code != http.StatusOK || !strings.Contains(route.Body.String(), `id="app"`) {
		t.Fatalf("SPA fallback: %d %s", route.Code, route.Body)
	}

	match := regexp.MustCompile(`src="(/assets/[^"]+\.js)"`).FindStringSubmatch(index.Body.String())
	if len(match) != 2 {
		t.Fatalf("UI index has no hashed script asset: %s", index.Body)
	}
	asset := call(h, "GET", match[1], "", "")
	contentType := asset.Header().Get("Content-Type")
	if asset.Code != http.StatusOK || (!strings.HasPrefix(contentType, "text/javascript") && !strings.HasPrefix(contentType, "application/javascript")) {
		t.Fatalf("UI asset: %d %s", asset.Code, contentType)
	}
	if !strings.Contains(asset.Header().Get("Cache-Control"), "immutable") {
		t.Fatal("UI asset is not immutably cacheable")
	}
}

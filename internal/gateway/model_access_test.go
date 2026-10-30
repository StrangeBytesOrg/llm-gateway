package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestModelAccessAcrossKeys(t *testing.T) {
	var calls atomic.Int64
	g, first, admin := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"id":"gen-test","choices":[],"usage":{"cost":0.001}}`)
	})
	ctx := context.Background()
	principal, err := g.Store.Authenticate(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := g.Store.CreateKey(ctx, principal.UserID, nil)
	if err != nil {
		t.Fatal(err)
	}
	g.Config.Models["advanced"] = testRoute()
	g.Config.ModelAccessGroups["premium"] = ModelAccessGroup{Models: []string{"alias", "advanced"}}
	g.Config.ModelAccessGroups["none"] = ModelAccessGroup{Models: []string{}}
	h, ah := g.InferenceHandler(), g.AdminHandler()
	path := "/admin/v1/users/" + principal.UserID
	checkModels := func(key, expected string) {
		t.Helper()
		w := call(h, "GET", "/v1/models", key, "")
		var list struct{ Data []struct{ ID string } }
		if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, model := range list.Data {
			names = append(names, model.ID)
		}
		if w.Code != 200 || strings.Join(names, ",") != expected {
			t.Fatalf("models: %d %s", w.Code, w.Body.String())
		}
	}
	chat := func(key, model string, status int) {
		t.Helper()
		w := call(h, "POST", "/v1/chat/completions", key, fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, model))
		if w.Code != status {
			t.Fatalf("chat: %d %s", w.Code, w.Body.String())
		}
		if status == 403 && !strings.Contains(w.Body.String(), "model_not_allowed") {
			t.Fatal(w.Body.String())
		}
	}
	update := func(body string) {
		t.Helper()
		w := call(ah, "PATCH", path, admin, body)
		if w.Code != 200 {
			t.Fatalf("update: %d %s", w.Code, w.Body.String())
		}
	}
	for _, key := range []string{first, second} {
		checkModels(key, "alias")
		chat(key, "advanced", 403)
		chat(key, "unknown", 403)
	}
	if calls.Load() != 0 {
		t.Fatal("denied requests reached upstream")
	}
	history, err := g.Store.History(ctx, principal.UserID, "", "", 100)
	if err != nil || len(history) != 0 {
		t.Fatalf("denied requests recorded: %v %v", history, err)
	}
	update(`{"model_access_group":"premium","budget_usd":"2"}`)
	for _, key := range []string{first, second} {
		checkModels(key, "advanced,alias")
		chat(key, "advanced", 200)
	}
	update(`{"model_access_group":"free"}`)
	for _, key := range []string{first, second} {
		checkModels(key, "alias")
		chat(key, "advanced", 403)
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls: %d", calls.Load())
	}
	history, err = g.Store.History(ctx, principal.UserID, "", "", 100)
	if err != nil || len(history) != 2 {
		t.Fatalf("history: %v %v", history, err)
	}
	for _, request := range history {
		if request.ModelAccessGroup != "premium" || request.Accounting != "settled" {
			t.Fatalf("lost admission snapshot: %+v", request)
		}
	}
	// Listing reflects permissions even when spending blocks inference.
	update(`{"budget_usd":"0"}`)
	checkModels(first, "alias")
	update(`{"model_access_group":"none"}`)
	checkModels(first, "")
	chat(first, "alias", 403)
	// Budget-only updates preserve the assignment.
	update(`{"budget_usd":"3"}`)
	user, _ := g.Store.User(ctx, principal.UserID)
	if user.ModelAccessGroup != "none" {
		t.Fatal(user)
	}
	for _, value := range []string{`null`, `""`, `"typo"`} {
		w := call(ah, "PATCH", path, admin, `{"budget_usd":"99","model_access_group":`+value+`}`)
		if w.Code < 400 {
			t.Fatalf("accepted group %s", value)
		}
		user, _ := g.Store.User(ctx, principal.UserID)
		if user.Budget != 3*USDScale {
			t.Fatalf("failed update changed budget: %+v", user)
		}
	}
}

func TestModelAccessCreationAndConfigReferences(t *testing.T) {
	g, _, admin := fixture(t, func(http.ResponseWriter, *http.Request) {})
	g.Config.ModelAccessGroups["empty"] = ModelAccessGroup{Models: []string{}}
	h := g.AdminHandler()
	body := `{"budget_usd":"1","cadence":"monthly","anchor":"2026-01-01T00:00:00Z"`
	create := func(suffix string, want string) {
		t.Helper()
		w := call(h, "POST", "/admin/v1/users", admin, body+suffix+"}")
		if want == "" {
			if w.Code < 400 {
				t.Fatal(w.Body.String())
			}
			return
		}
		var u User
		if err := json.Unmarshal(w.Body.Bytes(), &u); err != nil || w.Code != 201 || u.ModelAccessGroup != want {
			t.Fatalf("create: %d %s %v", w.Code, w.Body.String(), err)
		}
	}
	create("", "free")
	usersBefore, err := g.Store.Users(context.Background(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	g.Config.DefaultModelAccessGroup = "empty"
	create("", "empty")
	for _, before := range usersBefore {
		after, err := g.Store.User(context.Background(), before.ID)
		if err != nil || after.ModelAccessGroup != before.ModelAccessGroup {
			t.Fatalf("default changed existing assignment: %+v %v", after, err)
		}
	}
	create(`,"model_access_group":"free"`, "free")
	for _, value := range []string{`null`, `""`, `"typo"`} {
		create(`,"model_access_group":`+value, "")
	}
	if err := g.Store.ValidateUserGroups(context.Background(), g.Config); err != nil {
		t.Fatal(err)
	}
	delete(g.Config.ModelAccessGroups, "free")
	if err := g.Store.ValidateUserGroups(context.Background(), g.Config); err == nil {
		t.Fatal("missing stored group accepted")
	}
	if w := call(h, "GET", "/health/ready", admin, ""); w.Code != 503 {
		t.Fatalf("ready: %d", w.Code)
	}
}

func TestModelAccessConfigValidation(t *testing.T) {
	g, _, _ := fixture(t, func(http.ResponseWriter, *http.Request) {})
	for _, tc := range []struct {
		name   string
		groups map[string]ModelAccessGroup
		def    string
		valid  bool
	}{
		{"valid", map[string]ModelAccessGroup{"free": {Models: []string{"alias"}}}, "free", true},
		{"empty", map[string]ModelAccessGroup{"free": {Models: []string{}}}, "free", true},
		{"missing groups", nil, "free", false},
		{"unknown default", map[string]ModelAccessGroup{"free": {Models: []string{}}}, "typo", false},
		{"unknown model", map[string]ModelAccessGroup{"free": {Models: []string{"typo"}}}, "free", false},
		{"duplicate", map[string]ModelAccessGroup{"free": {Models: []string{"alias", "alias"}}}, "free", false},
		{"missing models", map[string]ModelAccessGroup{"free": {}}, "free", false},
		{"numeric name", map[string]ModelAccessGroup{"1": {Models: []string{}}}, "1", true},
		{"digit prefix", map[string]ModelAccessGroup{"2026-premium": {Models: []string{"alias"}}}, "2026-premium", true},
		{"hyphen prefix", map[string]ModelAccessGroup{"-premium": {Models: []string{}}}, "-premium", false},
		{"invalid name", map[string]ModelAccessGroup{"Free": {Models: []string{}}}, "Free", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := g.Config
			c.ModelAccessGroups = tc.groups
			c.DefaultModelAccessGroup = tc.def
			if err := c.Validate(); (err == nil) != tc.valid {
				t.Fatalf("validation: %v", err)
			}
		})
	}
}

func TestDowngradeAllowsAdmittedRequestToSettle(t *testing.T) {
	started, finish := make(chan struct{}), make(chan struct{})
	g, key, admin := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-finish
		fmt.Fprint(w, `{"id":"gen-test","choices":[],"usage":{"cost":0.001}}`)
	})
	g.Config.ModelAccessGroups["none"] = ModelAccessGroup{Models: []string{}}
	h := g.InferenceHandler()
	done := make(chan int, 1)
	go func() { done <- call(h, "POST", "/v1/chat/completions", key, `{"model":"alias","messages":[{}]}`).Code }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(finish)
		t.Fatal("upstream did not start")
	}
	p, _ := g.Store.Authenticate(context.Background(), key)
	w := call(g.AdminHandler(), "PATCH", "/admin/v1/users/"+p.UserID, admin, `{"model_access_group":"none"}`)
	close(finish)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if status := <-done; status != 200 {
		t.Fatalf("in-flight request: %d", status)
	}
	request := onlyRequest(t, g)
	if request.ModelAccessGroup != "free" || request.Accounting != "settled" {
		t.Fatalf("settlement: %+v", request)
	}
	if w := call(h, "POST", "/v1/chat/completions", key, `{"model":"alias","messages":[{}]}`); w.Code != 403 {
		t.Fatalf("new request: %d", w.Code)
	}
}

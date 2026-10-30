package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestBudgetPatchPreservesSpendingWithoutHistory(t *testing.T) {
	g, key, admin := fixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected upstream call") })
	ctx := context.Background()
	principal, err := g.Store.Authenticate(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	u, err := g.Store.User(ctx, principal.UserID)
	if err != nil {
		t.Fatal(err)
	}
	g.Config.Models["advanced"] = testRoute()
	g.Config.ModelAccessGroups["premium"] = ModelAccessGroup{Models: []string{"alias", "advanced"}}
	now := time.Now()
	admit := func(model string) Request {
		t.Helper()
		r, err := g.Store.Admit(ctx, key, model, testRoute(), Provider{}, now, g.Config)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	first, late := admit("alias"), admit("alias")
	if err = g.Store.Settle(ctx, first.ID, 8*USDScale, "8", nil, "provider_usage", "", false); err != nil {
		t.Fatal(err)
	}
	before, err := g.Store.Summary(ctx, u.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	audits, err := g.Store.AuditLog(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	h := g.AdminHandler()
	path := "/admin/v1/users/" + u.ID
	patch := func(body string) User {
		t.Helper()
		w := call(h, "PATCH", path, admin, body)
		var result User
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatalf("PATCH: %d %s", w.Code, w.Body)
		}
		return result
	}
	result := patch(`{"budget_usd":"9","model_access_group":"premium"}`)
	if result.PolicyID != u.PolicyID || result.AllowanceID != u.AllowanceID || result.BudgetUSD != "9.000000000" || result.ModelAccessGroup != "premium" || result.Cadence != u.Cadence || result.Anchor != u.Anchor {
		t.Fatalf("in-place update: %+v", result)
	}
	after, err := g.Store.Summary(ctx, u.ID, now)
	if err != nil || !reflect.DeepEqual(before.Current, after.Current) || len(after.Periods) != 1 {
		t.Fatalf("spending changed: %+v %v", after, err)
	}
	fresh := admit("advanced")
	if fresh.PeriodID != first.PeriodID || fresh.PolicyID != u.PolicyID || fresh.ModelAccessGroup != "premium" {
		t.Fatalf("updated admission: %+v", fresh)
	}
	old, err := g.Store.Request(ctx, first.ID)
	if err != nil || old.ModelAccessGroup != u.ModelAccessGroup {
		t.Fatalf("old request changed: %+v %v", old, err)
	}
	if err = g.Store.Settle(ctx, late.ID, 2*USDScale, "2", nil, "provider_usage", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err = g.Store.Admit(ctx, key, "advanced", testRoute(), Provider{}, now, g.Config); !errors.Is(err, ErrBudget) {
		t.Fatalf("late charge ignored: %v", err)
	}
	patch(`{"budget_usd":"12"}`)
	admit("advanced")
	patch(`{"budget_usd":"7"}`)
	after, err = g.Store.Summary(ctx, u.ID, now)
	if err != nil || after.Current.SpentUSD != "10.000000000" {
		t.Fatalf("spending truncated: %+v %v", after, err)
	}
	if _, err = g.Store.Admit(ctx, key, "advanced", testRoute(), Provider{}, now, g.Config); !errors.Is(err, ErrBudget) {
		t.Fatalf("lower limit: %v", err)
	}
	// Applying the same update again changes no identities and requires no retry ID.
	patch(`{"budget_usd":"7"}`)
	result = patch(`{"enabled":false,"budget_usd":"12","model_access_group":"free"}`)
	if result.Enabled || result.ModelAccessGroup != "free" {
		t.Fatalf("combined enablement: %+v", result)
	}
	if _, err = g.Store.Admit(ctx, key, "alias", testRoute(), Provider{}, now, g.Config); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled admission: %v", err)
	}
	patch(`{"enabled":true}`)
	// Natural renewal uses the updated recurring limit and the original schedule.
	_, end, err := Period(time.Unix(u.Anchor, 0), now, u.Cadence)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := g.Store.Admit(ctx, key, "alias", testRoute(), Provider{}, end, g.Config)
	if err != nil || renewed.PeriodID == first.PeriodID || renewed.AllowanceID != u.AllowanceID {
		t.Fatalf("renewal: %+v %v", renewed, err)
	}
	policies, err := g.Store.BudgetPolicies(ctx, u.ID, "", 100)
	if err != nil || len(policies) != 1 || policies[0].ID != u.PolicyID || policies[0].BudgetUSD != "12.000000000" {
		t.Fatalf("unexpected policy history: %+v %v", policies, err)
	}
	afterAudits, err := g.Store.AuditLog(ctx, 0, 100)
	if err != nil || !reflect.DeepEqual(audits, afterAudits) {
		t.Fatalf("PATCH created audit history: %+v %v", afterAudits, err)
	}
	var transitions int
	if err = g.Store.db.QueryRow("SELECT count(*) FROM budget_transitions").Scan(&transitions); err != nil || transitions != 0 {
		t.Fatalf("PATCH created retry records: %d %v", transitions, err)
	}
}

func TestBudgetPatchValidationAndRollback(t *testing.T) {
	g, key, admin := fixture(t, func(http.ResponseWriter, *http.Request) {})
	ctx := context.Background()
	p, err := g.Store.Authenticate(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	original, err := g.Store.User(ctx, p.UserID)
	if err != nil {
		t.Fatal(err)
	}
	h := g.AdminHandler()
	path := "/admin/v1/users/" + p.UserID
	for _, body := range []string{
		`{"budget_usd":"-1","enabled":false}`, `{"budget_usd":"invalid","enabled":false}`,
		`{"budget_usd":"99","model_access_group":"missing"}`, `{"budget_usd":"99","model_access_group":null}`,
		`{"budget_usd":"99","model_access_group":""}`, `{"anchor":"2026-01-01T00:00:00Z"}`, `{"cadence":"weekly"}`,
		`{"budget_usd":"9","expected_policy_id":"unused"}`, `{}`,
	} {
		w := call(h, "PATCH", path, admin, body)
		if w.Code != 400 && w.Code != 422 {
			t.Fatalf("invalid PATCH: %d %s", w.Code, w.Body)
		}
		actual, err := g.Store.User(ctx, p.UserID)
		if err != nil || !reflect.DeepEqual(actual, original) {
			t.Fatalf("invalid PATCH partially applied: %+v %v", actual, err)
		}
	}
	g.Config.ModelAccessGroups["premium"] = ModelAccessGroup{Models: []string{"alias"}}
	// Fail after the policy amount is written, both during UPDATE and at COMMIT.
	for _, stage := range []string{"update", "commit"} {
		t.Run(stage, func(t *testing.T) {
			trigger := `CREATE TRIGGER fail_patch BEFORE UPDATE ON users BEGIN SELECT RAISE(ABORT,'injected update failure'); END`
			if stage == "commit" {
				trigger = `CREATE TABLE patch_commit_guard(user_id TEXT REFERENCES users(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER fail_patch AFTER UPDATE ON users BEGIN INSERT INTO patch_commit_guard VALUES('missing'); END`
			}
			if _, err = g.Store.db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			body := `{"budget_usd":"9","model_access_group":"premium","enabled":false}`
			w := call(h, "PATCH", path, admin, body)
			if w.Code != 503 {
				t.Fatalf("injected failure: %d %s", w.Code, w.Body)
			}
			actual, err := g.Store.User(ctx, p.UserID)
			if err != nil || !reflect.DeepEqual(actual, original) {
				t.Fatalf("partial PATCH: %+v %v", actual, err)
			}
			if _, err = g.Store.db.Exec("DROP TRIGGER fail_patch"); err != nil {
				t.Fatal(err)
			}
		})
	}
	if w := call(h, "PATCH", path, admin, `{"budget_usd":"0","model_access_group":"premium"}`); w.Code != 200 {
		t.Fatalf("valid PATCH: %d %s", w.Code, w.Body)
	}
	if _, err = g.Store.Admit(ctx, key, "alias", testRoute(), Provider{}, time.Now(), g.Config); !errors.Is(err, ErrBudget) {
		t.Fatalf("zero budget: %v", err)
	}
	if w := call(h, "POST", path+"/budget-adjustments", admin, `{}`); w.Code != 404 {
		t.Fatalf("removed adjustment endpoint: %d %s", w.Code, w.Body)
	}
}

func TestBudgetPatchDoesNotChangeTransitionReplay(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, _ := provision(t, s, "10")
	in := transitionFor(u, "subscription")
	transitioned, err := s.TransitionBudget(ctx, u.ID, in, testAccessConfig())
	if err != nil {
		t.Fatal(err)
	}
	budget := 9 * USDScale
	enabled := false
	patched, err := s.UpdateUser(ctx, u.ID, &enabled, &budget, nil)
	if err != nil {
		t.Fatal(err)
	}
	if patched.PolicyID != transitioned.PolicyID {
		t.Fatal("PATCH revised the policy")
	}
	replay, err := s.TransitionBudget(ctx, u.ID, in, testAccessConfig())
	if err != nil || !reflect.DeepEqual(replay, transitioned) {
		t.Fatalf("transition replay: %+v %v", replay, err)
	}
	actual, err := s.User(ctx, u.ID)
	if err != nil || !reflect.DeepEqual(actual, patched) {
		t.Fatalf("replay reverted PATCH: %+v %v", actual, err)
	}
	// PATCH intentionally leaves the transition precondition unchanged.
	if _, err = s.TransitionBudget(ctx, u.ID, transitionFor(transitioned, "next-subscription"), testAccessConfig()); err != nil {
		t.Fatalf("transition after PATCH: %v", err)
	}
}

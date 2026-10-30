package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func transitionFor(u User, event string) BudgetTransition {
	return BudgetTransition{TransitionID: event, ExpectedPolicyID: u.PolicyID, BudgetUSD: "20", Cadence: u.Cadence, Anchor: time.Unix(u.Anchor, 0).UTC()}
}

func TestBudgetTransitionAccounting(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	u, key := provision(t, s, "1")
	now := time.Now()
	admit := func() Request {
		t.Helper()
		r, err := s.Admit(ctx, key, "alias", testRoute(), Provider{Accounting: "openrouter"}, now, testAccessConfig())
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	first := admit()
	late := admit()
	if err := s.Settle(ctx, first.ID, USDScale, "1", nil, "provider_usage", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Admit(ctx, key, "alias", testRoute(), Provider{}, now, testAccessConfig()); !errors.Is(err, ErrBudget) {
		t.Fatalf("exhausted allowance: %v", err)
	}
	// Identical boundaries must still create an independent allowance.
	input := transitionFor(u, "upgrade")
	next, err := s.TransitionBudget(ctx, u.ID, input, testAccessConfig())
	if err != nil {
		t.Fatal(err)
	}
	fresh := admit()
	if first.PeriodStart != fresh.PeriodStart || first.PeriodID == fresh.PeriodID {
		t.Fatal("period identities not isolated")
	}
	if err = s.Settle(ctx, late.ID, USDScale/2, "0.5", nil, "provider_usage", "", false); err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(ctx, first.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.Settle(ctx, first.ID, USDScale/4, "0.25", nil, "", "correction", true); err != nil {
		t.Fatal(err)
	}
	if err = s.Settle(ctx, fresh.ID, USDScale/10, "0.1", nil, "provider_usage", "", false); err != nil {
		t.Fatal(err)
	}
	summary, err := s.Summary(ctx, u.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Current.AllowanceID != next.AllowanceID || summary.Current.SpentUSD != "0.100000000" || len(summary.Periods) != 2 {
		t.Fatalf("summary: %+v", summary)
	}
	for _, p := range summary.Periods {
		if p.AllowanceID == u.AllowanceID && p.SpentUSD != "0.750000000" {
			t.Fatalf("old allowance: %+v", p)
		}
	}
	// A cadence change with the same anchor must not reuse an old end boundary.
	input = transitionFor(next, "weekly")
	input.Cadence = "weekly"
	weekly, err := s.TransitionBudget(ctx, u.ID, input, testAccessConfig())
	if err != nil {
		t.Fatal(err)
	}
	req := admit()
	summary, err = s.Summary(ctx, u.ID, now)
	if err != nil || summary.Current.AllowanceID != weekly.AllowanceID || summary.Current.End-summary.Current.Start != 604800 || req.PeriodID == fresh.PeriodID {
		t.Fatalf("weekly period: %+v %v", summary, err)
	}
	policies, err := s.BudgetPolicies(ctx, u.ID, "", 100)
	if err != nil || len(policies) != 3 {
		t.Fatalf("policies: %+v %v", policies, err)
	}
	for _, p := range policies {
		if p.ID == u.PolicyID && p.BudgetUSD != "1.000000000" {
			t.Fatal("historical policy overwritten")
		}
	}
}

func TestBudgetTransitionReplayAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gateway.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	u, err := s.CreateUser(ctx, "replay", USDScale, "monthly", time.Now().AddDate(0, -1, 0), "free")
	if err != nil {
		t.Fatal(err)
	}
	in := transitionFor(u, "event-1")
	group := "premium"
	disabled := false
	in.ModelAccessGroup = &group
	in.Enabled = &disabled
	config := testAccessConfig()
	config.ModelAccessGroups["premium"] = ModelAccessGroup{Models: []string{"alias"}}
	first, err := s.TransitionBudget(ctx, u.ID, in, config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.TransitionBudget(ctx, u.ID, transitionFor(first, "event-2"), config)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	delete(config.ModelAccessGroups, "premium")
	replay, err := s.TransitionBudget(ctx, u.ID, in, config)
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatalf("durable replay: %+v %v", replay, err)
	}
	current, err := s.User(ctx, u.ID)
	if err != nil || current.PolicyID != second.PolicyID {
		t.Fatalf("replay changed current policy: %+v %v", current, err)
	}
	conflict := in
	conflict.BudgetUSD = "21"
	if _, err = s.TransitionBudget(ctx, u.ID, conflict, config); !errors.Is(err, ErrTransitionConflict) {
		t.Fatalf("changed retry: %v", err)
	}
	stale := transitionFor(u, "older-event")
	if _, err = s.TransitionBudget(ctx, u.ID, stale, config); !errors.Is(err, ErrStalePolicy) {
		t.Fatalf("stale event: %v", err)
	}
	entries, err := s.AuditLog(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range entries {
		if e.Action == "budget.transition" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("replay audit entries: %d", count)
	}
}

func TestBudgetTransitionConcurrency(t *testing.T) {
	for _, sameEvent := range []bool{true, false} {
		t.Run(map[bool]string{true: "duplicate", false: "competing"}[sameEvent], func(t *testing.T) {
			s := testStore(t)
			u, _ := provision(t, s, "1")
			ctx := context.Background()
			var wg sync.WaitGroup
			results := make(chan error, 12)
			for range 12 {
				wg.Go(func() {
					in := transitionFor(u, "event")
					if !sameEvent {
						in.TransitionID = ID("event_")
					}
					_, err := s.TransitionBudget(ctx, u.ID, in, testAccessConfig())
					results <- err
				})
			}
			wg.Wait()
			close(results)
			success := 0
			for err := range results {
				if err == nil {
					success++
				} else if !errors.Is(err, ErrStalePolicy) {
					t.Fatal(err)
				}
			}
			expected := 1
			if sameEvent {
				expected = 12
			}
			if success != expected {
				t.Fatalf("successes: %d", success)
			}
			policies, err := s.BudgetPolicies(ctx, u.ID, "", 100)
			if err != nil || len(policies) != 2 {
				t.Fatalf("extra allowance: %d %v", len(policies), err)
			}
		})
	}
}

func TestBudgetTransitionRollback(t *testing.T) {
	for _, stage := range []string{"audit", "commit"} {
		t.Run(stage, func(t *testing.T) {
			s := testStore(t)
			u, _ := provision(t, s, "1")
			ctx := context.Background()
			trigger := `CREATE TRIGGER fail_transition BEFORE INSERT ON audit WHEN NEW.action='budget.transition' BEGIN SELECT RAISE(ABORT,'injected audit failure'); END`
			if stage == "commit" {
				trigger = `CREATE TABLE commit_guard(user_id TEXT REFERENCES users(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER fail_transition AFTER INSERT ON audit WHEN NEW.action='budget.transition' BEGIN INSERT INTO commit_guard VALUES('missing'); END`
			}
			if _, err := s.db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			in := transitionFor(u, "retryable")
			group := "premium"
			enabled := false
			in.ModelAccessGroup = &group
			in.Enabled = &enabled
			config := testAccessConfig()
			config.ModelAccessGroups["premium"] = ModelAccessGroup{}
			if _, err := s.TransitionBudget(ctx, u.ID, in, config); err == nil {
				t.Fatal("injected failure succeeded")
			}
			actual, err := s.User(ctx, u.ID)
			if err != nil || !reflect.DeepEqual(actual, u) {
				t.Fatalf("partial user update: %+v %v", actual, err)
			}
			for table, want := range map[string]int{"budget_policies": 1, "budget_allowances": 1, "budget_transitions": 0} {
				var count int
				if err = s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != want {
					t.Fatalf("partial %s: %d %v", table, count, err)
				}
			}
			if _, err = s.db.Exec("DROP TRIGGER fail_transition"); err != nil {
				t.Fatal(err)
			}
			if _, err = s.TransitionBudget(ctx, u.ID, in, config); err != nil {
				t.Fatalf("retry after rollback: %v", err)
			}
		})
	}
}

func TestBudgetPolicyDatabaseConstraints(t *testing.T) {
	s := testStore(t)
	u, key := provision(t, s, "1")
	ctx := context.Background()
	r, err := s.Admit(ctx, key, "alias", testRoute(), Provider{}, time.Now(), testAccessConfig())
	if err != nil {
		t.Fatal(err)
	}
	other, _ := provision(t, s, "2")
	for _, q := range []string{
		"UPDATE budget_policies SET allowance_id=allowance_id",
		"UPDATE budget_allowances SET anchor=anchor+1",
		"DELETE FROM budget_allowances",
		"UPDATE requests SET policy_id=policy_id",
		"DELETE FROM budget_policies",
		"UPDATE periods SET end=end+1",
		"UPDATE requests SET period_id=period_id",
		"UPDATE users SET current_policy_id='missing'",
		"UPDATE users SET current_policy_id='" + other.PolicyID + "' WHERE id='" + u.ID + "'",
	} {
		if _, err = s.db.Exec(q); err == nil {
			t.Fatalf("constraint accepted: %s", q)
		}
	}
	// Ownership and allowance membership are enforced on insertion as well as updates.
	if _, err = s.db.Exec("INSERT INTO budget_policies(id,user_id,budget,created,allowance_id) VALUES(?,?,?,?,?)", ID("pol_"), u.ID, 1, time.Now().Unix(), other.AllowanceID); err == nil {
		t.Fatal("accepted another user's allowance")
	}
	for _, allowance := range []string{u.AllowanceID, other.AllowanceID} {
		_, err = s.db.Exec(`INSERT INTO requests(id,user_id,key_id,model_access_group,model,provider,upstream_model,route,created,period_start,period_id,policy_id,allowance_id)
   SELECT ?,user_id,key_id,model_access_group,model,provider,upstream_model,route,created,period_start,period_id,?,? FROM requests WHERE id=?`, ID("req_"), other.PolicyID, allowance, r.ID)
		if err == nil {
			t.Fatal("accepted mismatched request policy or allowance")
		}
	}
	// Ledger sums and period totals remain reconcilable after identity protections.
	if err = s.Settle(ctx, r.ID, 1, "0.000000001", nil, "provider_usage", "", false); err != nil {
		t.Fatal(err)
	}
}

func TestAdminBudgetTransitions(t *testing.T) {
	g, key, admin := fixture(t, func(http.ResponseWriter, *http.Request) {})
	ctx := context.Background()
	p, err := g.Store.Authenticate(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	u, err := g.Store.User(ctx, p.UserID)
	if err != nil {
		t.Fatal(err)
	}
	h := g.AdminHandler()
	path := "/admin/v1/users/" + u.ID + "/budget-transitions"
	in := transitionFor(u, "subscription-1")
	group := "premium"
	in.ModelAccessGroup = &group
	g.Config.ModelAccessGroups[group] = ModelAccessGroup{Models: []string{"alias"}}
	encoded, _ := json.Marshal(in)
	w := call(h, "POST", path, admin, string(encoded))
	var result User
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.PolicyID == u.PolicyID || result.ModelAccessGroup != group {
		t.Fatalf("transition: %d %s", w.Code, w.Body)
	}
	replay := call(h, "POST", path, admin, string(encoded))
	if replay.Code != 200 || replay.Body.String() != w.Body.String() {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
	in.TransitionID = "stale-event"
	encoded, _ = json.Marshal(in)
	if w = call(h, "POST", path, admin, string(encoded)); w.Code != 409 {
		t.Fatalf("stale event: %d %s", w.Code, w.Body)
	}
	in = transitionFor(result, "invalid-group")
	bad := "missing"
	in.ModelAccessGroup = &bad
	encoded, _ = json.Marshal(in)
	if w = call(h, "POST", path, admin, string(encoded)); w.Code != 400 {
		t.Fatalf("invalid group: %d %s", w.Code, w.Body)
	}
	actual, err := g.Store.User(ctx, u.ID)
	if err != nil || actual.PolicyID != result.PolicyID {
		t.Fatalf("invalid transition changed policy: %+v %v", actual, err)
	}
	changed := transitionFor(result, "subscription-1")
	changed.BudgetUSD = "999"
	encoded, _ = json.Marshal(changed)
	if w = call(h, "POST", path, admin, string(encoded)); w.Code != 409 {
		t.Fatalf("conflicting retry: %d %s", w.Code, w.Body)
	}
	for _, body := range []string{`{"model_access_group":null}`, `{}`, `{"transition_id":"x"}`, `{"transition_id":"x","expected_policy_id":"y","budget_usd":"1","cadence":"daily","anchor":"2026-01-01T00:00:00Z"}`} {
		if w = call(h, "POST", path, admin, body); w.Code != 422 {
			t.Fatalf("validation: %d %s", w.Code, w.Body)
		}
	}
	if w = call(h, "PATCH", "/admin/v1/users/"+u.ID, admin, `{"budget_usd":"5"}`); w.Code != 200 {
		t.Fatalf("administrative budget update: %d %s", w.Code, w.Body)
	}
	w = call(h, "GET", "/admin/v1/users/"+u.ID+"/budget-policies?limit=1", admin, "")
	var page Page[BudgetPolicy]
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Data) != 1 || page.NextCursor == "" {
		t.Fatalf("policy history: %d %s", w.Code, w.Body)
	}
	w = call(h, "GET", "/admin/v1/users/"+u.ID+"/budget-policies?after="+page.NextCursor, admin, "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Data) != 1 {
		t.Fatalf("policy pagination: %d %s", w.Code, w.Body)
	}
}

func TestBudgetTransitionAdmissionBoundaries(t *testing.T) {
	s := testStore(t)
	u, key := provision(t, s, "1")
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	in := transitionFor(u, "future")
	in.Anchor = now.Add(time.Hour + 123*time.Nanosecond)
	next, err := s.TransitionBudget(ctx, u.ID, in, testAccessConfig())
	if err != nil {
		t.Fatal(err)
	}
	if next.Anchor != in.Anchor.Unix() {
		t.Fatal("anchor precision not truncated")
	}
	if _, err = s.Admit(ctx, key, "alias", testRoute(), Provider{}, now, testAccessConfig()); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("future anchor: %v", err)
	}
	if _, err = s.Admit(ctx, key, "alias", testRoute(), Provider{}, time.Unix(next.Anchor, 0), testAccessConfig()); err != nil {
		t.Fatalf("anchor boundary: %v", err)
	}
	in = transitionFor(next, "cancel")
	in.BudgetUSD = "0"
	in.Anchor = now
	next, err = s.TransitionBudget(ctx, u.ID, in, testAccessConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Admit(ctx, key, "alias", testRoute(), Provider{}, now, testAccessConfig()); !errors.Is(err, ErrBudget) {
		t.Fatalf("zero policy: %v", err)
	}
	for _, invalid := range []BudgetTransition{
		{TransitionID: "x", ExpectedPolicyID: next.PolicyID, BudgetUSD: "-1", Cadence: "monthly", Anchor: now},
		{TransitionID: "x", ExpectedPolicyID: next.PolicyID, BudgetUSD: "1", Cadence: "daily", Anchor: now},
		{TransitionID: "x", ExpectedPolicyID: next.PolicyID, BudgetUSD: "1", Cadence: "monthly"},
		{TransitionID: " ", ExpectedPolicyID: next.PolicyID, BudgetUSD: "1", Cadence: "monthly", Anchor: now},
	} {
		if _, err = s.TransitionBudget(ctx, u.ID, invalid, testAccessConfig()); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf("invalid policy: %v", err)
		}
	}
	actual, err := s.User(ctx, u.ID)
	if err != nil || actual.PolicyID != next.PolicyID {
		t.Fatalf("invalid transition changed policy: %+v %v", actual, err)
	}
}

func TestCurrentBudgetUsageOutsideHistoryLimit(t *testing.T) {
	s := testStore(t)
	u, key := provision(t, s, "1")
	ctx := context.Background()
	now := time.Now()
	in := transitionFor(u, "earlier-anchor")
	in.Anchor = time.Unix(0, 0)
	in.Cadence = "weekly"
	current, err := s.TransitionBudget(ctx, u.ID, in, testAccessConfig())
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Admit(ctx, key, "alias", testRoute(), Provider{}, now, testAccessConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Settle(ctx, r.ID, USDScale/2, "0.5", nil, "provider_usage", "", false); err != nil {
		t.Fatal(err)
	}
	// Historical policies can have starts later than the newly selected schedule.
	// The history cap must never make the actual current total disappear.
	for i := range 101 {
		if _, err = s.db.Exec("INSERT INTO periods(id,user_id,allowance_id,start,end) VALUES(?,?,?,?,?)", ID("per_"), u.ID, u.AllowanceID, now.Unix()+int64(i), now.Unix()+int64(i)+1); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := s.Summary(ctx, u.ID, now)
	if err != nil || len(summary.Periods) != 100 || summary.Current.AllowanceID != current.AllowanceID || summary.Current.ID != r.PeriodID || summary.Current.SpentUSD != "0.500000000" {
		t.Fatalf("current usage lost: %+v %v", summary, err)
	}
}

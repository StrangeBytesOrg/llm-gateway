package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func provision(t *testing.T, s *Store, budget string) (User, string) {
	t.Helper()
	n, err := ParseUSD(budget)
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUser(context.Background(), ID("ext_"), n, "monthly", time.Now().UTC().AddDate(0, 0, -1).Truncate(time.Second), "free")
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := s.CreateKey(context.Background(), u.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	return u, key
}
func testRoute() Route { return Route{Provider: "upstream", Model: "model"} }

func TestConcurrentSettlementAndOvershoot(t *testing.T) {
	s := testStore(t)
	u, key := provision(t, s, "1")
	ctx := context.Background()
	requests := make([]Request, 16)
	var wg sync.WaitGroup
	for i := range requests {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := s.Admit(ctx, key, "alias", testRoute(), Provider{Accounting: "openrouter"}, time.Now(), testAccessConfig())
			if err != nil {
				t.Error(err)
				return
			}
			requests[i] = r
		}(i)
	}
	wg.Wait()
	// Every admitted call may finish even though together they exceed the limit.
	for _, r := range requests {
		wg.Add(1)
		go func(r Request) {
			defer wg.Done()
			for range 3 {
				if err := s.Settle(ctx, r.ID, 100_000_000, "0.1", json.RawMessage(`{"cost":0.1}`), "provider_usage", "", false); err != nil {
					t.Error(err)
				}
			}
		}(r)
	}
	wg.Wait()
	summary, err := s.Summary(ctx, u.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Current.SpentUSD != "1.600000000" {
		t.Fatalf("spending: %+v", summary)
	}
	if _, err = s.Admit(ctx, key, "alias", testRoute(), Provider{Accounting: "openrouter"}, time.Now(), testAccessConfig()); !errors.Is(err, ErrBudget) {
		t.Fatalf("expected exhausted budget: %v", err)
	}
	var count int
	if err = s.db.QueryRow("SELECT count(*) FROM ledger").Scan(&count); err != nil || count != 16 {
		t.Fatalf("ledger count %d %v", count, err)
	}
}

func TestLateSettlementAndCorrection(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	anchor := stamp("2025-01-31T08:00:00Z")
	u, err := s.CreateUser(ctx, "late", USDScale, "monthly", anchor, "free")
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := s.CreateKey(ctx, u.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Admit(ctx, key, "alias", testRoute(), Provider{Accounting: "openrouter"}, stamp("2025-02-28T07:59:59Z"), testAccessConfig())
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.Admit(ctx, key, "alias", testRoute(), Provider{Accounting: "openrouter"}, stamp("2025-02-28T08:00:00Z"), testAccessConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(ctx, before.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.Settle(ctx, before.ID, USDScale, "1", nil, "provider_usage", "", false); err != nil {
		t.Fatal(err)
	}
	if err = s.Settle(ctx, before.ID, USDScale/2, "0.5", nil, "", "correction", true); err != nil {
		t.Fatal(err)
	}
	if err = s.Settle(ctx, before.ID, USDScale/2, "0.5", nil, "", "retry correction", true); err != nil {
		t.Fatal(err)
	}
	summary, err := s.Summary(ctx, u.ID, stamp("2025-02-28T09:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Current.SpentUSD != "0.000000000" || len(summary.Periods) != 2 || summary.Periods[1].SpentUSD != "0.500000000" {
		t.Fatalf("wrong attribution: %+v", summary)
	}
	if before.PeriodStart == after.PeriodStart {
		t.Fatal("boundary did not renew")
	}
	if err = s.Settle(ctx, after.ID, 0, "0", nil, "", "cannot resolve active request", true); !errors.Is(err, ErrConflict) {
		t.Fatalf("manual active resolution allowed: %v", err)
	}
}

func TestKeyLifecycleAndBudgetUpdate(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, key := provision(t, s, "0")
	if _, err := s.Admit(ctx, key, "alias", testRoute(), Provider{Accounting: "openrouter"}, time.Now(), testAccessConfig()); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
	if _, err := s.TransitionBudget(ctx, u.ID, BudgetTransition{TransitionID: "activate", ExpectedPolicyID: u.PolicyID, BudgetUSD: "1", Cadence: u.Cadence, Anchor: time.Unix(u.Anchor, 0)}, testAccessConfig()); err != nil {
		t.Fatal(err)
	}
	p, err := s.Authenticate(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, err = s.UpdateUser(ctx, u.ID, &disabled, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Admit(ctx, key, "alias", testRoute(), Provider{Accounting: "openrouter"}, time.Now(), testAccessConfig()); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	enabled := true
	s.UpdateUser(ctx, u.ID, &enabled, nil, nil)
	if err = s.RevokeKey(ctx, p.KeyID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, key); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	expired := time.Now().Add(-time.Second).Unix()
	_, old, err := s.CreateKey(ctx, u.ID, &expired)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, old); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	var stored string
	s.db.QueryRow("SELECT hash FROM virtual_keys WHERE id=?", p.KeyID).Scan(&stored)
	if stored == key || len(stored) != 64 {
		t.Fatal("key secret persisted")
	}
}

func TestRecoveryBackupAndDatabaseOwnership(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if other, err := OpenStore(path); err == nil {
		other.Close()
		t.Fatal("second owner accepted")
	}
	u, key := provision(t, s, "10")
	req, err := s.Admit(ctx, key, "alias", testRoute(), Provider{Accounting: "openrouter"}, time.Now(), testAccessConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Generation(ctx, req.ID, "gen-test"); err != nil {
		t.Fatal(err)
	}
	if err = s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := s.Request(ctx, req.ID)
	if err != nil || r.Lifecycle != "interrupted" || r.Accounting != "pending" || r.GenerationID != "gen-test" {
		t.Fatalf("recovery: %+v %v", r, err)
	}
	backup := filepath.Join(dir, "backup.db")
	if err = s.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if err = VerifyBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStore(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err = restored.User(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Backup(ctx, backup); err == nil {
		t.Fatal("overwrote backup")
	}
}

func TestHistoryPagination(t *testing.T) {
	s := testStore(t)
	_, key := provision(t, s, "1")
	ctx := context.Background()
	for range 7 {
		if _, err := s.Admit(ctx, key, "alias", testRoute(), Provider{Accounting: "openrouter"}, time.Now(), testAccessConfig()); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		rows, err := s.History(ctx, "", "", cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if seen[r.ID] {
				t.Fatal("duplicate page entry")
			}
			seen[r.ID] = true
		}
		if len(rows) < 2 {
			break
		}
		cursor = rows[len(rows)-1].ID
	}
	if len(seen) != 7 {
		t.Fatalf("found %d records", len(seen))
	}
}

func TestSettlementOverflowRollsBack(t *testing.T) {
	s := testStore(t)
	_, key := provision(t, s, "9223372036.854775807")
	ctx := context.Background()
	a, err := s.Admit(ctx, key, "alias", testRoute(), Provider{Accounting: "openrouter"}, time.Now(), testAccessConfig())
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Admit(ctx, key, "alias", testRoute(), Provider{Accounting: "openrouter"}, time.Now(), testAccessConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Settle(ctx, a.ID, 1<<63-1, "9223372036.854775807", nil, "provider_usage", "", false); err != nil {
		t.Fatal(err)
	}
	if err = s.Settle(ctx, b.ID, 1, "0.000000001", nil, "provider_usage", "", false); err == nil {
		t.Fatal("overflow accepted")
	}
	r, err := s.Request(ctx, b.ID)
	if err != nil || r.Accounting != "pending" {
		t.Fatalf("partial settlement: %+v %v", r, err)
	}
}

func testAccessConfig() Config {
	return Config{Models: map[string]Route{"alias": testRoute()}, ModelAccessGroups: map[string]ModelAccessGroup{"free": {Models: []string{"alias"}}}, DefaultModelAccessGroup: "free"}
}

package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func keyUser(t *testing.T, s *Store, id string) User {
	t.Helper()
	u, err := s.CreateUser(context.Background(), id, 10*USDScale, "monthly", time.Now().AddDate(0, 0, -1), "free")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func assertCredential(t *testing.T, s *Store, secret string, valid bool) {
	t.Helper()
	_, err := s.Authenticate(context.Background(), secret)
	if valid && err != nil {
		t.Fatalf("credential should authenticate: %v", err)
	}
	if !valid && !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("credential should be rejected: %v", err)
	}
}

func TestKeyAliasRotationPreservesAccounting(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u := keyUser(t, s, "hub-history")
	expires := time.Now().Add(time.Hour).Unix()
	old, oldSecret, err := s.CreateKeyWithAlias(ctx, u.ID, "alias-1", &expires)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != "active" || old.Rotated != nil {
		t.Fatalf("initial metadata: %+v", old)
	}
	// A past creation timestamp makes accidental replacement of the original
	// timestamp observable without sleeps or an injected production clock.
	old.Created = time.Now().Add(-time.Hour).Unix()
	if _, err = s.db.Exec("UPDATE virtual_keys SET created=? WHERE id=?", old.Created, old.ID); err != nil {
		t.Fatal(err)
	}
	request, err := s.Admit(ctx, oldSecret, "alias", testRoute(), Provider{Accounting: "reported"}, time.Now(), testAccessConfig())
	if err != nil {
		t.Fatal(err)
	}
	replacement, secret, err := s.RotateKey(ctx, u.ID, old.Alias, nil)
	if err != nil {
		t.Fatal(err)
	}
	if secret == oldSecret || replacement.ID == old.ID || replacement.Alias != old.Alias || replacement.Created != old.Created || replacement.Rotated == nil || replacement.Expires != nil || replacement.Status != "active" {
		t.Fatalf("replacement metadata: old=%+v new=%+v", old, replacement)
	}
	assertCredential(t, s, oldSecret, false)
	assertCredential(t, s, secret, true)
	// The old credential ID cannot accidentally revoke its replacement.
	if err = s.RevokeKey(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	assertCredential(t, s, secret, true)
	if err = s.Finish(ctx, request.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.Settle(ctx, request.ID, 300_000_000, "0.3", json.RawMessage(`{"cost":0.3}`), "provider_usage", "", false); err != nil {
		t.Fatal(err)
	}
	got, err := s.Request(ctx, request.ID)
	if err != nil || got.KeyID != old.ID || got.Accounting != "settled" {
		t.Fatalf("old accounting link: %+v %v", got, err)
	}
	ledger, err := s.Ledger(ctx, request.ID, 0, 10)
	if err != nil || len(ledger) != 1 || ledger[0].AmountUSD != "0.300000000" {
		t.Fatalf("old ledger: %+v %v", ledger, err)
	}
	keys, err := s.Keys(ctx, u.ID, "", 100)
	if err != nil || len(keys) != 1 || keys[0].ID != replacement.ID {
		t.Fatalf("history appeared in key list: %+v %v", keys, err)
	}
	var revoked, current int
	if err = s.db.QueryRow("SELECT revoked,is_current FROM virtual_keys WHERE id=?", old.ID).Scan(&revoked, &current); err != nil || revoked != 1 || current != 0 {
		t.Fatalf("old credential not retired: %d %d %v", revoked, current, err)
	}
	// Observe through an independent connection, so a returned secret cannot be
	// backed merely by uncommitted changes on the writer connection.
	reader, err := sql.Open("sqlite", s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var hash string
	if err = reader.QueryRow("SELECT hash FROM virtual_keys WHERE id=? AND revoked=0 AND is_current=1", replacement.ID).Scan(&hash); err != nil || hash != keyHash(secret) {
		t.Fatalf("returned credential was not committed: %v", err)
	}
}

func TestKeyAliasUniquenessRevocationAndRetry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := keyUser(t, s, "hub-a")
	b := keyUser(t, s, "hub-b")
	first, lostSecret, err := s.RotateKey(ctx, a.ID, "alias", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, secret, err := s.CreateKeyWithAlias(ctx, a.ID, "alias", nil); !errors.Is(err, ErrConflict) || secret != "" {
		t.Fatalf("duplicate creation: secret returned=%v error=%v", secret != "", err)
	}
	other, otherSecret, err := s.CreateKeyWithAlias(ctx, b.ID, "alias", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Retrying after a lost response intentionally produces another credential.
	second, secret, err := s.RotateKey(ctx, a.ID, "alias", nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("retry did not rotate")
	}
	assertCredential(t, s, lostSecret, false)
	assertCredential(t, s, secret, true)
	assertCredential(t, s, otherSecret, true)
	for range 2 {
		if err = s.RevokeKeyByAlias(ctx, a.ID, "alias"); err != nil {
			t.Fatal(err)
		}
	}
	assertCredential(t, s, secret, false)
	assertCredential(t, s, otherSecret, true)
	got, err := s.KeyByAlias(ctx, a.ID, "alias")
	if err != nil || got.Status != "revoked" || !got.Revoked || got.Created != first.Created || got.Rotated == nil {
		t.Fatalf("revoked metadata: %+v %v", got, err)
	}
	if _, _, err = s.CreateKeyWithAlias(ctx, a.ID, "alias", nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("revoked name was not reserved: %v", err)
	}
	if err = s.RevokeKeyByAlias(ctx, a.ID, "absent"); err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeKeyByAlias(ctx, "absent-user", "alias"); err != nil {
		t.Fatal(err)
	}
	if got, err = s.KeyByAlias(ctx, b.ID, "alias"); err != nil || got.ID != other.ID || got.Status != "active" {
		t.Fatalf("cross-user revocation: %+v %v", got, err)
	}
	reactivated, fresh, err := s.RotateKey(ctx, a.ID, "alias", nil)
	if err != nil {
		t.Fatal(err)
	}
	if reactivated.Created != first.Created || reactivated.Status != "active" {
		t.Fatalf("reactivation: %+v", reactivated)
	}
	assertCredential(t, s, fresh, true)
	assertCredential(t, s, secret, false)
}

func TestConcurrentKeyAliasRotations(t *testing.T) {
	s := testStore(t)
	u := keyUser(t, s, "hub-concurrent")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	const n = 24
	issued := make([]CreateKeyResult, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			issued[i].Key, issued[i].Secret, errs[i] = s.RotateKey(ctx, u.ID, "same-alias", nil)
		}(i)
	}
	close(start)
	wg.Wait()
	current, err := s.KeyByAlias(ctx, u.ID, "same-alias")
	if err != nil {
		t.Fatal(err)
	}
	active := 0
	for i, result := range issued {
		if errs[i] != nil {
			t.Errorf("rotation %d: %v", i, errs[i])
			continue
		}
		p, err := s.Authenticate(ctx, result.Secret)
		if err == nil {
			active++
			if p.KeyID != current.ID {
				t.Error("superseded credential remained active")
			}
		} else if !errors.Is(err, ErrUnauthorized) {
			t.Error(err)
		}
	}
	if active != 1 {
		t.Fatalf("active returned credentials: %d", active)
	}
	var rows, currentRows, activeRows int
	if err = s.db.QueryRow("SELECT count(*),sum(is_current),sum(CASE WHEN revoked=0 THEN 1 ELSE 0 END) FROM virtual_keys WHERE user_id=? AND alias=?", u.ID, "same-alias").Scan(&rows, &currentRows, &activeRows); err != nil || rows != n || currentRows != 1 || activeRows != 1 {
		t.Fatalf("credential invariant: rows=%d current=%d active=%d err=%v", rows, currentRows, activeRows, err)
	}
	keys, err := s.Keys(ctx, u.ID, "", 100)
	if err != nil || len(keys) != 1 {
		t.Fatalf("duplicate logical keys: %d %v", len(keys), err)
	}
	// The database also enforces the invariant independently of the Go code.
	if _, err = s.db.Exec("INSERT INTO virtual_keys(id,user_id,hash,prefix,created,alias) VALUES(?,?,?,?,?,?)", ID("key_"), u.ID, keyHash("duplicate-constraint-test"), "gw_test", time.Now().Unix(), "same-alias"); err == nil {
		t.Fatal("database accepted a second current credential")
	}
}

func TestKeyAliasRotationRollback(t *testing.T) {
	for _, phase := range []string{"replacement insert", "audit insert", "commit"} {
		t.Run(phase, func(t *testing.T) {
			s := testStore(t)
			ctx := context.Background()
			u := keyUser(t, s, "hub-rollback")
			old, oldSecret, err := s.CreateKeyWithAlias(ctx, u.ID, "alias-rollback", nil)
			if err != nil {
				t.Fatal(err)
			}
			switch phase {
			case "replacement insert":
				_, err = s.db.Exec(`CREATE TRIGGER fail_rotation BEFORE INSERT ON virtual_keys WHEN NEW.alias='alias-rollback' BEGIN SELECT RAISE(ABORT,'injected replacement failure'); END`)
			case "audit insert":
				_, err = s.db.Exec(`CREATE TRIGGER fail_rotation BEFORE INSERT ON audit WHEN NEW.action='key.rotate' BEGIN SELECT RAISE(ABORT,'injected audit failure'); END`)
			case "commit":
				_, err = s.db.Exec(`CREATE TABLE rotation_commit_guard(user_id TEXT REFERENCES users(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER fail_rotation AFTER INSERT ON audit WHEN NEW.action='key.rotate' BEGIN INSERT INTO rotation_commit_guard(user_id) VALUES('missing-parent'); END`)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, secret, err := s.RotateKey(ctx, u.ID, "alias-rollback", nil); err == nil || secret != "" {
				t.Fatalf("failed rotation returned a secret or succeeded: secret returned=%v error=%v", secret != "", err)
			}
			assertCredential(t, s, oldSecret, true)
			got, err := s.KeyByAlias(ctx, u.ID, "alias-rollback")
			if err != nil || got.ID != old.ID || got.Status != "active" || got.Rotated != nil {
				t.Fatalf("rollback changed metadata: %+v %v", got, err)
			}
			var versions, rotations int
			if err = s.db.QueryRow("SELECT count(*) FROM virtual_keys WHERE user_id=?", u.ID).Scan(&versions); err != nil || versions != 1 {
				t.Fatalf("failed rotation left a version: %d %v", versions, err)
			}
			if err = s.db.QueryRow("SELECT count(*) FROM audit WHERE action='key.rotate'").Scan(&rotations); err != nil || rotations != 0 {
				t.Fatalf("failed rotation left an audit event: %d %v", rotations, err)
			}
		})
	}
}

func TestKeyAliasRevocationRollback(t *testing.T) {
	s := testStore(t)
	u := keyUser(t, s, "hub-revoke-rollback")
	ctx := context.Background()
	_, secret, err := s.RotateKey(ctx, u.ID, "alias", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER fail_revoke BEFORE INSERT ON audit WHEN NEW.action='key.revoke' BEGIN SELECT RAISE(ABORT,'injected revoke failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeKeyByAlias(ctx, u.ID, "alias"); err == nil {
		t.Fatal("revocation succeeded without audit")
	}
	assertCredential(t, s, secret, true)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, replacement, err := s.RotateKey(cancelled, u.ID, "alias", nil); err == nil || replacement != "" {
		t.Fatal("cancelled rotation returned a secret")
	}
	assertCredential(t, s, secret, true)
}

func TestKeyAliasStatusesAndPagination(t *testing.T) {
	s := testStore(t)
	u := keyUser(t, s, "hub-status")
	ctx := context.Background()
	expired := time.Now().Add(-time.Minute).Unix()
	old, _, err := s.CreateKeyWithAlias(ctx, u.ID, "expired", &expired)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.KeyByAlias(ctx, u.ID, "expired"); err != nil || got.Status != "expired" {
		t.Fatalf("expired status: %+v %v", got, err)
	}
	if k, secret, err := s.RotateKey(ctx, u.ID, "expired", nil); err != nil || k.Status != "active" {
		t.Fatalf("expired key did not reactivate: %+v %v", k, err)
	} else {
		assertCredential(t, s, secret, true)
	}
	for i := range 5 {
		if _, _, err := s.RotateKey(ctx, u.ID, fmt.Sprintf("alias-%d", i), nil); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	after := ""
	for {
		keys, err := s.Keys(ctx, u.ID, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, k := range keys {
			if seen[k.Alias] || k.ID == old.ID {
				t.Fatal("historical or duplicate key in page")
			}
			seen[k.Alias] = true
		}
		if len(keys) < 2 {
			break
		}
		after = keys[len(keys)-1].ID
	}
	if len(seen) != 6 {
		t.Fatalf("logical key count: %d", len(seen))
	}
}

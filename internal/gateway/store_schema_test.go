package gateway

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestInitialSchemaAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 1 {
		t.Fatalf("initial schema version: %d %v", version, err)
	}
	ctx := context.Background()
	u := keyUser(t, s, "hub-reopen")
	original, oldSecret, err := s.CreateKeyWithAlias(ctx, u.ID, "alias-reopen", nil)
	if err != nil {
		t.Fatal(err)
	}
	provider := Provider{Accounting: "reported", BaseURL: "https://example.invalid/v1", APIKeyEnv: "PROVIDER_KEY"}
	request, err := s.Admit(ctx, oldSecret, "alias", testRoute(), provider, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	current, secret, err := s.RotateKey(ctx, u.ID, original.Alias, nil)
	if err != nil {
		t.Fatal(err)
	}
	revoked, revokedSecret, err := s.CreateKeyWithAlias(ctx, u.ID, "alias-revoked", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeKeyByAlias(ctx, u.ID, revoked.Alias); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 1 {
		t.Fatalf("reopened schema version: %d %v", version, err)
	}
	if got, err := reopened.User(ctx, u.ID); err != nil || got.ID != u.ID {
		t.Fatalf("reopening lost user: %+v %v", got, err)
	}
	assertCredential(t, reopened, oldSecret, false)
	assertCredential(t, reopened, secret, true)
	assertCredential(t, reopened, revokedSecret, false)
	got, err := reopened.KeyByAlias(ctx, u.ID, original.Alias)
	if err != nil || got.ID != current.ID || got.Created != original.Created || got.Rotated == nil || *got.Rotated != *current.Rotated {
		t.Fatalf("reopening changed current key: %+v %v", got, err)
	}
	if got, err := reopened.KeyByAlias(ctx, u.ID, revoked.Alias); err != nil || got.Status != "revoked" {
		t.Fatalf("reopening reactivated revoked key: %+v %v", got, err)
	}
	stored, err := reopened.Request(ctx, request.ID)
	if err != nil || stored.KeyID != original.ID || stored.ProviderConfig != provider {
		t.Fatalf("reopening lost accounting relationships or provider snapshot: %+v %v", stored, err)
	}
	if err := reopened.Finish(ctx, request.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Settle(ctx, request.ID, 100_000_000, "0.1", nil, "provider_usage", "", false); err != nil {
		t.Fatal(err)
	}
}

func TestInitialSchemaFailureIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partial.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Initialization reaches this table last. A conflict must roll back every
	// earlier table/index and preserve data that existed before initialization.
	if _, err := db.Exec("CREATE TABLE audit(marker TEXT); INSERT INTO audit VALUES('preserve-me')"); err != nil {
		t.Fatal(err)
	}
	if s, err := OpenStore(path); err == nil {
		s.Close()
		t.Fatal("initialization unexpectedly succeeded over a conflicting table")
	}
	var version, createdTables int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 0 {
		t.Fatalf("failed initialization changed version: %d %v", version, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type='table' AND name<>'audit'").Scan(&createdTables); err != nil || createdTables != 0 {
		t.Fatalf("partial schema survived rollback: %d %v", createdTables, err)
	}
	var marker string
	if err := db.QueryRow("SELECT marker FROM audit").Scan(&marker); err != nil || marker != "preserve-me" {
		t.Fatalf("failed initialization changed existing data: %q %v", marker, err)
	}
}

func TestRejectNewerSchemaWithoutChangingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "newer.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA user_version=2; CREATE TABLE marker(value TEXT); INSERT INTO marker VALUES('preserve-me')"); err != nil {
		t.Fatal(err)
	}
	if s, err := OpenStore(path); err == nil {
		s.Close()
		t.Fatal("accepted a database newer than schema version 1")
	}
	var value string
	if err := db.QueryRow("SELECT value FROM marker").Scan(&value); err != nil || value != "preserve-me" {
		t.Fatalf("rejected database data changed: %q %v", value, err)
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Fatalf("rejected database version changed: %d %v", version, err)
	}
	var name string
	if err := db.QueryRow("SELECT name FROM sqlite_schema WHERE name='users'").Scan(&name); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("schema was created in a rejected database: %q %v", name, err)
	}
}

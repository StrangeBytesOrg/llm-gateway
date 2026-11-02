package gateway

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/StrangeBytesOrg/llm-gateway/internal/gateway/db"
	"github.com/pressly/goose/v3"
	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store owns the database lifecycle and the sqlc-generated query set. Domain
// operations live in store_*.go; pure rules and models live in the matching
// domain files (users.go, keys.go, budget.go, requests.go).
type Store struct {
	db   *sql.DB
	q    *db.Queries
	lock *os.File
	path string
}

func OpenStore(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(abs+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("database already owned by another gateway: %w", err)
	}
	fail := func(e error) (*Store, error) { lock.Close(); return nil, e }
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fail(err)
	}
	f.Close()
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(FULL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	sqlDB, err := sql.Open("sqlite", u.String())
	if err != nil {
		return fail(err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	s := &Store{db: sqlDB, q: db.New(sqlDB), lock: lock, path: abs}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = s.migrate(ctx); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	err := s.db.Close()
	e := s.lock.Close()
	if err != nil {
		return err
	}
	return e
}

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// transaction runs fn against the store's generated queries inside one
// transaction. The serialized writer connection makes rollback the default for
// every failed operation.
func (s *Store) transaction(ctx context.Context, fn func(*db.Queries) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(s.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

func newMigrationProvider(sqlDB *sql.DB) (*goose.Provider, error) {
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectSQLite3, sqlDB, fsys)
}

func (s *Store) migrate(ctx context.Context) error {
	provider, err := newMigrationProvider(s.db)
	if err != nil {
		return err
	}
	sources := provider.ListSources()
	latest := sources[len(sources)-1].Version
	current, err := provider.GetDBVersion(ctx)
	if err != nil {
		return err
	}
	if current > latest {
		return errors.New("database schema is newer than this binary")
	}
	_, err = provider.Up(ctx)
	return err
}

func ID(prefix string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}

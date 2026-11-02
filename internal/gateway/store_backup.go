package gateway

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
)

func (s *Store) Backup(ctx context.Context, dest string) error {
	abs, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// Use a separate read connection so a snapshot doesn't monopolize the
	// serialized writer connection used for admission and settlement.
	u := url.URL{Scheme: "file", Path: s.path}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	reader, e := sql.Open("sqlite", u.String())
	if e != nil {
		os.Remove(abs)
		return e
	}
	defer reader.Close()
	// VACUUM INTO produces a consistent snapshot without copying a live WAL file.
	if _, err = reader.ExecContext(ctx, "VACUUM INTO ?", abs); err != nil {
		os.Remove(abs)
		return err
	}
	f, err = os.OpenFile(abs, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func VerifyBackup(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	sqlDB, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	var result string
	if err = sqlDB.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return errors.New("backup integrity check failed")
	}
	rows, err := sqlDB.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("backup foreign key check failed")
	}
	return rows.Err()
}

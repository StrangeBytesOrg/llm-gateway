package gateway

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"time"
)

var ErrInvalidKeyAlias = errors.New("invalid_key_alias")

type Key struct {
	ID      string `json:"id" doc:"Internal ID of the current credential version; changes on rotation."`
	UserID  string `json:"user_id" doc:"Immutable user ID, caller-supplied or gateway-generated."`
	Alias   string `json:"alias" doc:"Key alias, unique within its user and matched exactly in key-alias routes."`
	Status  string `json:"status" enum:"active,revoked,expired" doc:"Credential state; user enablement and budget are checked separately."`
	Prefix  string `json:"prefix"`
	Expires *int64 `json:"expires_unix"`
	Revoked bool   `json:"revoked"`
	Created int64  `json:"created_unix" doc:"Original creation time for this user's key alias, preserved across rotation."`
	Rotated *int64 `json:"rotated_unix" doc:"Most recent successful rotate operation, including create-on-rotate; null if never rotated."`
}

const keyCols = "id,user_id,alias,prefix,expires,revoked,created,rotated"

func keyStatus(k Key, now int64) string {
	if k.Revoked {
		return "revoked"
	}
	if k.Expires != nil && *k.Expires <= now {
		return "expired"
	}
	return "active"
}

func scanKey(row scanner) (Key, error) {
	var k Key
	err := row.Scan(&k.ID, &k.UserID, &k.Alias, &k.Prefix, &k.Expires, &k.Revoked, &k.Created, &k.Rotated)
	k.Status = keyStatus(k, time.Now().Unix())
	return k, err
}

func validKeyAlias(id string) bool { return strings.TrimSpace(id) != "" && len(id) <= 256 }

// keyTransaction holds a dedicated connection through commit. A failed SQLite
// commit can leave a transaction open (for example, a deferred FK violation).
// Discard that connection rather than return uncommitted credential changes to
// the pool. Closing it rolls back the failed transaction.
func (s *Store) keyTransaction(ctx context.Context, apply func(*sql.Tx) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if e := tx.Rollback(); e != nil && !errors.Is(e, sql.ErrTxDone) {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	if err = apply(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	return err
}

func newKeyCredential(user, alias string, expires *int64) (Key, string) {
	secret := ID("gw_") + ID("")
	k := Key{ID: ID("key_"), UserID: user, Alias: alias, Prefix: secret[:11], Expires: expires, Created: time.Now().Unix()}
	if k.Alias == "" {
		k.Alias = k.ID
	}
	k.Status = keyStatus(k, time.Now().Unix())
	return k, secret
}

func insertKey(ctx context.Context, tx *sql.Tx, k Key, secret string) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO virtual_keys("+keyCols+",hash) VALUES(?,?,?,?,?,?,?,?,?)", k.ID, k.UserID, k.Alias, k.Prefix, k.Expires, k.Revoked, k.Created, k.Rotated, keyHash(secret))
	return err
}

// CreateKey generates a key alias for callers that don't supply one.
// The alias is stored explicitly, never matched as an internal-ID fallback.
func (s *Store) CreateKey(ctx context.Context, user string, expires *int64) (Key, string, error) {
	return s.createKey(ctx, user, "", expires)
}

func (s *Store) CreateKeyWithAlias(ctx context.Context, user, alias string, expires *int64) (Key, string, error) {
	if !validKeyAlias(alias) {
		return Key{}, "", ErrInvalidKeyAlias
	}
	return s.createKey(ctx, user, alias, expires)
}

func (s *Store) createKey(ctx context.Context, user, alias string, expires *int64) (Key, string, error) {
	k, secret := newKeyCredential(user, alias, expires)
	err := s.keyTransaction(ctx, func(tx *sql.Tx) error {
		var found int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE id=?", user).Scan(&found); err != nil {
			return err
		}
		if found == 0 {
			return ErrNotFound
		}
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM virtual_keys WHERE user_id=? AND alias=? AND is_current=1", user, k.Alias).Scan(&found); err != nil {
			return err
		}
		if found != 0 {
			return ErrConflict
		}
		k.Created = time.Now().Unix()
		if err := insertKey(ctx, tx, k, secret); err != nil {
			return err
		}
		return audit(ctx, tx, "key.create", k.ID, k)
	})
	if err != nil {
		return Key{}, "", err
	}
	k.Status = keyStatus(k, time.Now().Unix())
	return k, secret, nil
}

func (s *Store) KeyByAlias(ctx context.Context, user, alias string) (Key, error) {
	k, err := scanKey(s.db.QueryRowContext(ctx, "SELECT "+keyCols+" FROM virtual_keys WHERE user_id=? AND alias=? AND is_current=1", user, alias))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return k, err
}

func (s *Store) Keys(ctx context.Context, user, after string, limit int) ([]Key, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+keyCols+" FROM virtual_keys WHERE user_id=? AND is_current=1 AND id>? ORDER BY id LIMIT ?", user, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// RotateKey retains every old credential row for request/accounting foreign
// keys. Revocation, replacement and auditing commit as one operation.
func (s *Store) RotateKey(ctx context.Context, user, alias string, expires *int64) (Key, string, error) {
	if !validKeyAlias(alias) {
		return Key{}, "", ErrInvalidKeyAlias
	}
	k, secret := newKeyCredential(user, alias, expires)
	err := s.keyTransaction(ctx, func(tx *sql.Tx) error {
		var userID string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=?", user).Scan(&userID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		old, err := scanKey(tx.QueryRowContext(ctx, "SELECT "+keyCols+" FROM virtual_keys WHERE user_id=? AND alias=? AND is_current=1", user, alias))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		now := time.Now().Unix()
		k.Created = now
		k.Rotated = &now
		if err == nil {
			k.Created = old.Created
			if _, err = tx.ExecContext(ctx, "UPDATE virtual_keys SET revoked=1,is_current=0 WHERE id=?", old.ID); err != nil {
				return err
			}
		}
		if err = insertKey(ctx, tx, k, secret); err != nil {
			return err
		}
		return audit(ctx, tx, "key.rotate", k.ID, map[string]any{"user_id": userID, "alias": alias, "previous_key_id": old.ID, "key": k})
	})
	if err != nil {
		return Key{}, "", err
	}
	k.Status = keyStatus(k, time.Now().Unix())
	return k, secret, nil
}

// RevokeKeyByAlias is idempotent even when the parent user or key is absent.
// Every successful request is audited, including no-ops, in the transaction.
func (s *Store) RevokeKeyByAlias(ctx context.Context, user, alias string) error {
	if !validKeyAlias(alias) {
		return ErrInvalidKeyAlias
	}
	return s.keyTransaction(ctx, func(tx *sql.Tx) error {
		var found string
		err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=?", user).Scan(&found)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		k := Key{}
		if err == nil {
			k, err = scanKey(tx.QueryRowContext(ctx, "SELECT "+keyCols+" FROM virtual_keys WHERE user_id=? AND alias=? AND is_current=1", user, alias))
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		absent := err != nil
		target := alias
		if !absent {
			target = k.ID
			if _, err = tx.ExecContext(ctx, "UPDATE virtual_keys SET revoked=1 WHERE id=?", k.ID); err != nil {
				return err
			}
		}
		return audit(ctx, tx, "key.revoke", target, map[string]any{"user_id": user, "alias": alias, "absent": absent, "already_revoked": k.Revoked})
	})
}

// RevokeKey identifies a specific credential version by its internal ID. It revokes
// that historical credential, never a subsequently rotated replacement.
func (s *Store) RevokeKey(ctx context.Context, id string) error {
	return s.keyTransaction(ctx, func(tx *sql.Tx) error {
		k, err := scanKey(tx.QueryRowContext(ctx, "SELECT "+keyCols+" FROM virtual_keys WHERE id=?", id))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE virtual_keys SET revoked=1 WHERE id=?", id); err != nil {
			return err
		}
		return audit(ctx, tx, "key.revoke", id, map[string]any{"user_id": k.UserID, "alias": k.Alias, "already_revoked": k.Revoked})
	})
}

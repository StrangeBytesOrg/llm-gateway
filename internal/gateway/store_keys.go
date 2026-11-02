package gateway

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"time"

	"github.com/StrangeBytesOrg/llm-gateway/internal/gateway/db"
)

func keyFromDB(k db.VirtualKey) Key {
	key := Key{
		ID:      k.ID,
		UserID:  k.UserID,
		Alias:   k.Alias,
		Prefix:  k.Prefix,
		Expires: k.Expires,
		Revoked: k.Revoked != 0,
		Created: k.Created,
		Rotated: k.Rotated,
	}
	key.Status = keyStatus(key, time.Now().Unix())
	return key
}

// keyTransaction holds a dedicated connection through commit. A failed SQLite
// commit can leave a transaction open (for example, a deferred FK violation).
// Discard that connection rather than return uncommitted credential changes to
// the pool. Closing it rolls back the failed transaction.
func (s *Store) keyTransaction(ctx context.Context, apply func(*db.Queries) error) error {
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
	if err = apply(s.q.WithTx(tx)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
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
	err := s.keyTransaction(ctx, func(q *db.Queries) error {
		exists, err := q.UserExists(ctx, user)
		if err != nil {
			return err
		}
		if exists == 0 {
			return ErrNotFound
		}
		_, err = q.GetKeyByAlias(ctx, db.GetKeyByAliasParams{UserID: user, Alias: k.Alias})
		if err == nil {
			return ErrConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err = q.CreateKey(ctx, db.CreateKeyParams{
			ID:      k.ID,
			UserID:  k.UserID,
			Alias:   k.Alias,
			Prefix:  k.Prefix,
			Expires: k.Expires,
			Revoked: boolInt(k.Revoked),
			Created: k.Created,
			Rotated: k.Rotated,
			Hash:    keyHash(secret),
		}); err != nil {
			return err
		}
		return createAudit(ctx, q, "key.create", k.ID, k)
	})
	if err != nil {
		return Key{}, "", err
	}
	k.Status = keyStatus(k, time.Now().Unix())
	return k, secret, nil
}

func (s *Store) KeyByAlias(ctx context.Context, user, alias string) (Key, error) {
	k, err := s.q.GetKeyByAlias(ctx, db.GetKeyByAliasParams{UserID: user, Alias: alias})
	if errors.Is(err, sql.ErrNoRows) {
		return Key{}, ErrNotFound
	}
	if err != nil {
		return Key{}, err
	}
	return keyFromDB(k), nil
}

func (s *Store) Keys(ctx context.Context, user, after string, limit int) ([]Key, error) {
	rows, err := s.q.ListKeys(ctx, db.ListKeysParams{UserID: user, ID: after, Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]Key, 0, len(rows))
	for _, row := range rows {
		out = append(out, keyFromDB(row))
	}
	return out, nil
}

// RotateKey retains every old credential row for request/accounting foreign
// keys. Revocation, replacement and auditing commit as one operation.
func (s *Store) RotateKey(ctx context.Context, user, alias string, expires *int64) (Key, string, error) {
	if !validKeyAlias(alias) {
		return Key{}, "", ErrInvalidKeyAlias
	}
	k, secret := newKeyCredential(user, alias, expires)
	err := s.keyTransaction(ctx, func(q *db.Queries) error {
		exists, err := q.UserExists(ctx, user)
		if err != nil {
			return err
		}
		if exists == 0 {
			return ErrNotFound
		}
		old, err := q.GetKeyByAlias(ctx, db.GetKeyByAliasParams{UserID: user, Alias: alias})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		now := time.Now().Unix()
		k.Created = now
		k.Rotated = &now
		if err == nil {
			k.Created = old.Created
			if err = q.RetireKey(ctx, old.ID); err != nil {
				return err
			}
		}
		if err = q.CreateKey(ctx, db.CreateKeyParams{
			ID:      k.ID,
			UserID:  k.UserID,
			Alias:   k.Alias,
			Prefix:  k.Prefix,
			Expires: k.Expires,
			Revoked: boolInt(k.Revoked),
			Created: k.Created,
			Rotated: k.Rotated,
			Hash:    keyHash(secret),
		}); err != nil {
			return err
		}
		return createAudit(ctx, q, "key.rotate", k.ID, map[string]any{
			"user_id":         user,
			"alias":           alias,
			"previous_key_id": old.ID,
			"key":             k,
		})
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
	return s.keyTransaction(ctx, func(q *db.Queries) error {
		exists, err := q.UserExists(ctx, user)
		if err != nil {
			return err
		}
		absent := exists == 0
		k := Key{}
		if !absent {
			row, err := q.GetKeyByAlias(ctx, db.GetKeyByAliasParams{UserID: user, Alias: alias})
			if err == nil {
				k = keyFromDB(row)
				if err = q.RevokeKey(ctx, k.ID); err != nil {
					return err
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			} else {
				absent = true
			}
		}
		target := alias
		if !absent {
			target = k.ID
		}
		return createAudit(ctx, q, "key.revoke", target, map[string]any{
			"user_id":         user,
			"alias":           alias,
			"absent":          absent,
			"already_revoked": k.Revoked,
		})
	})
}

// RevokeKey identifies a specific credential version by its internal ID. It revokes
// that historical credential, never a subsequently rotated replacement.
func (s *Store) RevokeKey(ctx context.Context, id string) error {
	return s.keyTransaction(ctx, func(q *db.Queries) error {
		row, err := q.GetKey(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		k := keyFromDB(row)
		if err = q.RevokeKey(ctx, id); err != nil {
			return err
		}
		return createAudit(ctx, q, "key.revoke", id, map[string]any{
			"user_id":         k.UserID,
			"alias":           k.Alias,
			"already_revoked": k.Revoked,
		})
	})
}

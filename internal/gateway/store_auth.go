package gateway

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/StrangeBytesOrg/llm-gateway/internal/gateway/db"
)

type Principal struct{ UserID, KeyID string }

func keyHash(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

func authenticate(ctx context.Context, q *db.Queries, hash string, now int64) (Principal, error) {
	row, err := q.AuthenticateKey(ctx, db.AuthenticateKeyParams{Hash: hash, Expires: &now})
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrUnauthorized
	}
	if err != nil {
		return Principal{}, err
	}
	if row.Enabled == 0 {
		return Principal{}, ErrDisabled
	}
	return Principal{UserID: row.UserID, KeyID: row.ID}, nil
}

func (s *Store) Authenticate(ctx context.Context, key string) (Principal, error) {
	return authenticate(ctx, s.q, keyHash(key), time.Now().Unix())
}

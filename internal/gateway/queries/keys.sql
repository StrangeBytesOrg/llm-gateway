-- name: GetKeyByAlias :one
SELECT id, user_id, hash, alias, prefix, expires, revoked, created, is_current, rotated
FROM virtual_keys
WHERE user_id = ? AND alias = ? AND is_current = 1;

-- name: GetKey :one
SELECT id, user_id, hash, alias, prefix, expires, revoked, created, is_current, rotated
FROM virtual_keys
WHERE id = ?;

-- name: ListKeys :many
SELECT id, user_id, hash, alias, prefix, expires, revoked, created, is_current, rotated
FROM virtual_keys
WHERE user_id = ? AND is_current = 1 AND id > ?
ORDER BY id
LIMIT ?;

-- name: CreateKey :exec
INSERT INTO virtual_keys(id, user_id, alias, prefix, expires, revoked, created, rotated, hash)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: RetireKey :exec
UPDATE virtual_keys
SET revoked = 1, is_current = 0
WHERE id = ?;

-- name: RevokeKey :exec
UPDATE virtual_keys
SET revoked = 1
WHERE id = ?;

-- name: RevokeCurrentKeyByAlias :exec
UPDATE virtual_keys
SET revoked = 1
WHERE user_id = ? AND alias = ? AND is_current = 1;

-- name: AuthenticateKey :one
SELECT k.user_id, k.id, u.enabled
FROM virtual_keys k
JOIN users u ON u.id = k.user_id
WHERE k.hash = ? AND k.is_current = 1 AND k.revoked = 0
  AND (k.expires IS NULL OR k.expires > ?);

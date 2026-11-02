-- name: CreateAudit :exec
INSERT INTO audit(action, target, detail, created)
VALUES (?, ?, ?, ?);

-- name: ListAudit :many
SELECT id, action, target, detail, created
FROM audit
WHERE id > ?
ORDER BY id
LIMIT ?;

-- name: ListAuditDesc :many
SELECT id, action, target, detail, created
FROM audit
WHERE id < ?
ORDER BY id DESC
LIMIT ?;

-- name: ListAuditDescAll :many
SELECT id, action, target, detail, created
FROM audit
ORDER BY id DESC
LIMIT ?;

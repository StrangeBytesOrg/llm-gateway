-- name: GetUser :one
SELECT
    u.id,
    u.alias,
    u.enabled,
    p.budget,
    a.cadence,
    a.anchor,
    u.created,
    u.model_access_group,
    u.current_policy_id,
    p.allowance_id
FROM users u
JOIN budget_policies p ON p.id = u.current_policy_id
JOIN budget_allowances a ON a.id = p.allowance_id
WHERE u.id = ?;

-- name: ListUsers :many
SELECT
    u.id,
    u.alias,
    u.enabled,
    p.budget,
    a.cadence,
    a.anchor,
    u.created,
    u.model_access_group,
    u.current_policy_id,
    p.allowance_id
FROM users u
JOIN budget_policies p ON p.id = u.current_policy_id
JOIN budget_allowances a ON a.id = p.allowance_id
WHERE u.id > ?
ORDER BY u.id
LIMIT ?;

-- name: UserExists :one
SELECT count(*)
FROM users
WHERE id = ?;

-- name: CreateUser :exec
INSERT INTO users(id, enabled, current_policy_id, created, model_access_group, alias)
VALUES (?, ?, ?, ?, ?, ?);

-- name: UpdateUser :exec
UPDATE users
SET enabled = ?, model_access_group = ?, alias = ?
WHERE id = ?;

-- name: UpdateUserPolicy :exec
UPDATE users
SET current_policy_id = ?, enabled = ?, model_access_group = ?
WHERE id = ?;

-- name: ListUserGroups :many
SELECT DISTINCT model_access_group
FROM users;

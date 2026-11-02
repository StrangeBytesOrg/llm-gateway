-- name: CreatePeriod :exec
INSERT INTO periods(id, user_id, allowance_id, start, end)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(allowance_id, start) DO NOTHING;

-- name: GetPeriodByAllowanceStart :one
SELECT id, spent
FROM periods
WHERE allowance_id = ? AND start = ?;

-- name: GetPeriodSpent :one
SELECT spent
FROM periods
WHERE user_id = ? AND id = ?;

-- name: AddPeriodSpent :exec
UPDATE periods
SET spent = spent + ?
WHERE user_id = ? AND id = ?;

-- name: ListUserPeriods :many
SELECT id, allowance_id, start, end, spent
FROM periods
WHERE user_id = ?
ORDER BY start DESC, id DESC
LIMIT 100;

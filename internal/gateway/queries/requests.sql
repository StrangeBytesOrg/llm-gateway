-- name: CreateRequest :exec
INSERT INTO requests(
    id, user_id, key_id, model, provider, upstream_model, route, created,
    period_start, period_id, policy_id, allowance_id, provider_config,
    model_access_group
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetRequest :one
SELECT
    id, user_id, key_id, model_access_group, model, provider, upstream_model,
    route, provider_config, generation_id, created, period_start, period_id,
    policy_id, allowance_id, lifecycle, accounting, cost, raw_cost, usage,
    attempts, next_attempt, error_code, finished
FROM requests
WHERE id = ?;

-- name: SetRequestGeneration :exec
UPDATE requests
SET generation_id = ?
WHERE id = ? AND generation_id = '';

-- name: FinishRequest :exec
UPDATE requests
SET lifecycle = ?, error_code = ?, finished = ?, next_attempt = ?
WHERE id = ?;

-- name: SetRequestUsage :exec
UPDATE requests
SET usage = ?
WHERE id = ? AND accounting != 'settled';

-- name: SettleRequest :exec
UPDATE requests
SET accounting = 'settled', cost = ?, raw_cost = ?, usage = ?
WHERE id = ?;

-- name: ListRequestHistory :many
SELECT
    id, user_id, key_id, model_access_group, model, provider, upstream_model,
    route, provider_config, generation_id, created, period_start, period_id,
    policy_id, allowance_id, lifecycle, accounting, cost, raw_cost, usage,
    attempts, next_attempt, error_code, finished
FROM requests
WHERE created < sqlc.arg('after_created')
   OR (created = sqlc.arg('after_created') AND id < sqlc.arg('after_id'))
ORDER BY created DESC, id DESC
LIMIT sqlc.arg('limit');

-- name: ListRequestHistoryByAccounting :many
SELECT
    id, user_id, key_id, model_access_group, model, provider, upstream_model,
    route, provider_config, generation_id, created, period_start, period_id,
    policy_id, allowance_id, lifecycle, accounting, cost, raw_cost, usage,
    attempts, next_attempt, error_code, finished
FROM requests
WHERE accounting = sqlc.arg('accounting')
  AND (created < sqlc.arg('after_created')
       OR (created = sqlc.arg('after_created') AND id < sqlc.arg('after_id')))
ORDER BY created DESC, id DESC
LIMIT sqlc.arg('limit');

-- name: ListUserRequestHistory :many
SELECT
    id, user_id, key_id, model_access_group, model, provider, upstream_model,
    route, provider_config, generation_id, created, period_start, period_id,
    policy_id, allowance_id, lifecycle, accounting, cost, raw_cost, usage,
    attempts, next_attempt, error_code, finished
FROM requests
WHERE user_id = sqlc.arg('user_id')
  AND (created < sqlc.arg('after_created')
       OR (created = sqlc.arg('after_created') AND id < sqlc.arg('after_id')))
ORDER BY created DESC, id DESC
LIMIT sqlc.arg('limit');

-- name: ListUserRequestHistoryByAccounting :many
SELECT
    id, user_id, key_id, model_access_group, model, provider, upstream_model,
    route, provider_config, generation_id, created, period_start, period_id,
    policy_id, allowance_id, lifecycle, accounting, cost, raw_cost, usage,
    attempts, next_attempt, error_code, finished
FROM requests
WHERE user_id = sqlc.arg('user_id')
  AND accounting = sqlc.arg('accounting')
  AND (created < sqlc.arg('after_created')
       OR (created = sqlc.arg('after_created') AND id < sqlc.arg('after_id')))
ORDER BY created DESC, id DESC
LIMIT sqlc.arg('limit');

-- name: ListPendingRequests :many
SELECT
    id, user_id, key_id, model_access_group, model, provider, upstream_model,
    route, provider_config, generation_id, created, period_start, period_id,
    policy_id, allowance_id, lifecycle, accounting, cost, raw_cost, usage,
    attempts, next_attempt, error_code, finished
FROM requests
WHERE accounting = 'pending' AND finished IS NOT NULL AND next_attempt <= ?
ORDER BY next_attempt, created
LIMIT ?;

-- name: RecoverRequests :exec
UPDATE requests
SET lifecycle = 'interrupted', finished = ?, error_code = 'process_interrupted',
    next_attempt = ?
WHERE finished IS NULL;

-- name: RecoverStaleRequests :exec
UPDATE requests
SET lifecycle = 'interrupted', finished = ?, error_code = 'finalization_interrupted',
    next_attempt = ?
WHERE finished IS NULL AND created < ?;

-- name: RetryRequest :exec
UPDATE requests
SET attempts = ?, next_attempt = ?, accounting = ?
WHERE id = ? AND accounting = 'pending';

-- name: CountAccounting :one
SELECT
    count(*) FILTER (WHERE accounting = 'pending') AS pending,
    count(*) FILTER (WHERE accounting = 'unresolved') AS unresolved
FROM requests;

-- name: CountAccountingForUser :one
SELECT
    count(*) FILTER (WHERE accounting = 'unresolved') AS unresolved,
    count(*) FILTER (WHERE accounting = 'pending') AS pending
FROM requests
WHERE user_id = ?;

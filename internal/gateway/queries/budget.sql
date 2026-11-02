-- name: CreateAllowance :exec
INSERT INTO budget_allowances(id, user_id, cadence, anchor)
VALUES (?, ?, ?, ?);

-- name: CreatePolicy :exec
INSERT INTO budget_policies(id, user_id, budget, created, allowance_id)
VALUES (?, ?, ?, ?, ?);

-- name: UpdatePolicyBudget :exec
UPDATE budget_policies
SET budget = ?
WHERE id = ?;

-- name: GetBudgetTransition :one
SELECT input, result
FROM budget_transitions
WHERE user_id = ? AND id = ?;

-- name: CreateBudgetTransition :exec
INSERT INTO budget_transitions(user_id, id, policy_id, input, result)
VALUES (?, ?, ?, ?, ?);

-- name: ListBudgetPolicies :many
SELECT p.id, p.user_id, p.budget, a.cadence, a.anchor, p.created, p.allowance_id
FROM budget_policies p
JOIN budget_allowances a ON a.id = p.allowance_id
WHERE p.user_id = ? AND p.id > ?
ORDER BY p.id
LIMIT ?;

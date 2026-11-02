-- name: CreateLedgerEntry :exec
INSERT INTO ledger(request_id, amount, source, reason, created)
VALUES (?, ?, ?, ?, ?);

-- name: ListLedger :many
SELECT id, request_id, amount, source, reason, created
FROM ledger
WHERE request_id = ? AND id > ?
ORDER BY id
LIMIT ?;

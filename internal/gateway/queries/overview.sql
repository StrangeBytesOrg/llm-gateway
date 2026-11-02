-- name: GetOverview :one
SELECT
    (SELECT count(*) FROM users) AS users,
    (SELECT count(*) FROM users WHERE enabled = 1) AS enabled_users,
    (SELECT count(*) FROM virtual_keys
        WHERE is_current = 1 AND revoked = 0
          AND (expires IS NULL OR expires > sqlc.arg('now'))) AS active_keys,
    (SELECT count(*) FROM requests r) AS requests,
    (SELECT count(*) FROM requests r WHERE r.accounting = 'pending') AS pending_requests,
    (SELECT count(*) FROM requests r WHERE r.accounting = 'unresolved') AS unresolved_requests,
    (SELECT count(*) FROM requests r WHERE r.created >= sqlc.arg('since')) AS requests_24h,
    CAST((SELECT coalesce(sum(l.amount), 0) FROM ledger l
        WHERE l.created >= sqlc.arg('since')) AS INTEGER) AS spend_24h;

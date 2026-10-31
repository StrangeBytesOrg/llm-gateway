package gateway

import (
	"context"
	"time"
)

type Overview struct {
	Users              int    `json:"users"`
	EnabledUsers       int    `json:"enabled_users"`
	ActiveKeys         int    `json:"active_keys"`
	Requests           int    `json:"requests"`
	PendingRequests    int    `json:"pending_requests"`
	UnresolvedRequests int    `json:"unresolved_requests"`
	Requests24h        int    `json:"requests_last_24h"`
	Spend24hUSD        string `json:"spend_last_24h_usd"`
}

func (s *Store) Overview(ctx context.Context, now time.Time) (Overview, error) {
	var o Overview
	var spend int64
	since := now.Add(-24 * time.Hour).Unix()
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM users),
		(SELECT count(*) FROM users WHERE enabled=1),
		(SELECT count(*) FROM virtual_keys WHERE is_current=1 AND revoked=0 AND (expires IS NULL OR expires>?)),
		(SELECT count(*) FROM requests),
		(SELECT count(*) FROM requests WHERE accounting='pending'),
		(SELECT count(*) FROM requests WHERE accounting='unresolved'),
		(SELECT count(*) FROM requests WHERE created>=?),
		(SELECT coalesce(sum(amount),0) FROM ledger WHERE created>=?)`,
		now.Unix(), since, since).Scan(
		&o.Users,
		&o.EnabledUsers,
		&o.ActiveKeys,
		&o.Requests,
		&o.PendingRequests,
		&o.UnresolvedRequests,
		&o.Requests24h,
		&spend,
	)
	if err != nil {
		return o, err
	}
	o.Spend24hUSD = FormatUSD(spend)
	return o, nil
}

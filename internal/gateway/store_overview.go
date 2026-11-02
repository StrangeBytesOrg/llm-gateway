package gateway

import (
	"context"
	"time"

	"github.com/StrangeBytesOrg/llm-gateway/internal/gateway/db"
)

func (s *Store) Overview(ctx context.Context, now time.Time) (Overview, error) {
	nowUnix := now.Unix()
	row, err := s.q.GetOverview(ctx, db.GetOverviewParams{
		Now:   &nowUnix,
		Since: now.Add(-24 * time.Hour).Unix(),
	})
	if err != nil {
		return Overview{}, err
	}
	return Overview{
		Users:              int(row.Users),
		EnabledUsers:       int(row.EnabledUsers),
		ActiveKeys:         int(row.ActiveKeys),
		Requests:           int(row.Requests),
		PendingRequests:    int(row.PendingRequests),
		UnresolvedRequests: int(row.UnresolvedRequests),
		Requests24h:        int(row.Requests24h),
		Spend24hUSD:        FormatUSD(row.Spend24h),
	}, nil
}

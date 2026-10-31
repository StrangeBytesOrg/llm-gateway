package gateway

import (
	"context"
	"encoding/json"
)

type LedgerEntry struct {
	ID        int64  `json:"id"`
	AmountUSD string `json:"amount_usd"`
	Source    string `json:"source"`
	Reason    string `json:"reason"`
	Created   int64  `json:"created_unix"`
}

func (s *Store) Ledger(ctx context.Context, request string, after int64, limit int) ([]LedgerEntry, error) {
	if _, err := s.Request(ctx, request); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,amount,source,reason,created FROM ledger WHERE request_id=? AND id>? ORDER BY id LIMIT ?", request, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LedgerEntry{}
	for rows.Next() {
		var e LedgerEntry
		var amount int64
		if err = rows.Scan(&e.ID, &amount, &e.Source, &e.Reason, &e.Created); err != nil {
			return nil, err
		}
		e.AmountUSD = FormatUSD(amount)
		out = append(out, e)
	}
	return out, rows.Err()
}

type AuditEntry struct {
	ID      int64           `json:"id"`
	Action  string          `json:"action"`
	Target  string          `json:"target"`
	Detail  json.RawMessage `json:"detail"`
	Created int64           `json:"created_unix"`
}

func (s *Store) AuditLog(ctx context.Context, after int64, limit int) ([]AuditEntry, error) {
	return s.auditLog(ctx, "WHERE id>? ORDER BY id LIMIT ?", after, limit)
}

// AuditLogDesc uses the same exclusive cursor for newest-first pagination.
func (s *Store) AuditLogDesc(ctx context.Context, before int64, limit int) ([]AuditEntry, error) {
	if before == 0 {
		return s.auditLog(ctx, "ORDER BY id DESC LIMIT ?", limit)
	}
	return s.auditLog(ctx, "WHERE id<? ORDER BY id DESC LIMIT ?", before, limit)
}

func (s *Store) auditLog(ctx context.Context, clause string, args ...any) ([]AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,action,target,detail,created FROM audit "+clause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var detail string
		if err = rows.Scan(&e.ID, &e.Action, &e.Target, &detail, &e.Created); err != nil {
			return nil, err
		}
		e.Detail = json.RawMessage(detail)
		out = append(out, e)
	}
	return out, rows.Err()
}

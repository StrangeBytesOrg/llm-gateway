package gateway

import (
	"context"
	"encoding/json"
	"time"

	"github.com/StrangeBytesOrg/llm-gateway/internal/gateway/db"
)

func createAudit(ctx context.Context, q *db.Queries, action, target string, detail any) error {
	encoded, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	return q.CreateAudit(ctx, db.CreateAuditParams{
		Action:  action,
		Target:  target,
		Detail:  string(encoded),
		Created: time.Now().Unix(),
	})
}

func (s *Store) Ledger(ctx context.Context, request string, after int64, limit int) ([]LedgerEntry, error) {
	if _, err := s.Request(ctx, request); err != nil {
		return nil, err
	}
	rows, err := s.q.ListLedger(ctx, db.ListLedgerParams{RequestID: request, ID: after, Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]LedgerEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, LedgerEntry{
			ID:        row.ID,
			AmountUSD: FormatUSD(row.Amount),
			Source:    row.Source,
			Reason:    row.Reason,
			Created:   row.Created,
		})
	}
	return out, nil
}

func auditFromDB(row db.Audit) AuditEntry {
	return AuditEntry{
		ID:      row.ID,
		Action:  row.Action,
		Target:  row.Target,
		Detail:  json.RawMessage(row.Detail),
		Created: row.Created,
	}
}

func (s *Store) AuditLog(ctx context.Context, after int64, limit int) ([]AuditEntry, error) {
	rows, err := s.q.ListAudit(ctx, db.ListAuditParams{ID: after, Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	return auditEntriesFromDB(rows), nil
}

// AuditLogDesc uses the same exclusive cursor for newest-first pagination.
func (s *Store) AuditLogDesc(ctx context.Context, before int64, limit int) ([]AuditEntry, error) {
	var (
		rows []db.Audit
		err  error
	)
	if before == 0 {
		rows, err = s.q.ListAuditDescAll(ctx, int64(limit))
	} else {
		rows, err = s.q.ListAuditDesc(ctx, db.ListAuditDescParams{ID: before, Limit: int64(limit)})
	}
	if err != nil {
		return nil, err
	}
	return auditEntriesFromDB(rows), nil
}

func auditEntriesFromDB(rows []db.Audit) []AuditEntry {
	out := make([]AuditEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, auditFromDB(row))
	}
	return out
}

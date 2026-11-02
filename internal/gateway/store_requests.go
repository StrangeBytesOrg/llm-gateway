package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/StrangeBytesOrg/llm-gateway/internal/gateway/db"
)

func requestFromDB(r db.Request) (Request, error) {
	out := Request{
		ModelAccessGroup: r.ModelAccessGroup,
		ID:               r.ID,
		UserID:           r.UserID,
		KeyID:            r.KeyID,
		Model:            r.Model,
		Provider:         r.Provider,
		UpstreamModel:    r.UpstreamModel,
		GenerationID:     r.GenerationID,
		Created:          r.Created,
		PeriodID:         r.PeriodID,
		PolicyID:         r.PolicyID,
		AllowanceID:      r.AllowanceID,
		PeriodStart:      r.PeriodStart,
		Lifecycle:        r.Lifecycle,
		Accounting:       r.Accounting,
		Cost:             r.Cost,
		RawCost:          r.RawCost,
		Usage:            json.RawMessage(r.Usage),
		Attempts:         int(r.Attempts),
		NextAttempt:      r.NextAttempt,
		ErrorCode:        r.ErrorCode,
		Finished:         r.Finished,
	}
	if err := json.Unmarshal([]byte(r.Route), &out.Route); err != nil {
		return Request{}, err
	}
	if err := json.Unmarshal([]byte(r.ProviderConfig), &out.ProviderConfig); err != nil {
		return Request{}, err
	}
	if r.Cost != nil {
		cost := FormatUSD(*r.Cost)
		out.CostUSD = &cost
	}
	return out, nil
}

func requestsFromDB(rows []db.Request) ([]Request, error) {
	out := make([]Request, 0, len(rows))
	for _, row := range rows {
		r, err := requestFromDB(row)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *Store) Admit(ctx context.Context, secret, model string, route Route, provider Provider, now time.Time, config Config) (Request, error) {
	r := Request{
		ID:             ID("req_"),
		Model:          model,
		Provider:       route.Provider,
		UpstreamModel:  route.Model,
		Route:          route,
		ProviderConfig: provider,
		Created:        now.Unix(),
		Lifecycle:      "admitted",
		Accounting:     "pending",
	}
	err := s.transaction(ctx, func(q *db.Queries) error {
		p, err := authenticate(ctx, q, keyHash(secret), now.Unix())
		if err != nil {
			return err
		}
		r.UserID = p.UserID
		r.KeyID = p.KeyID
		u, err := getUser(ctx, q, p.UserID)
		if err != nil {
			return err
		}
		if !config.AllowsModel(u.ModelAccessGroup, model) {
			return ErrModelNotAllowed
		}
		r.ModelAccessGroup = u.ModelAccessGroup
		r.PolicyID = u.PolicyID
		r.AllowanceID = u.AllowanceID
		start, end, err := Period(time.Unix(u.Anchor, 0), now, u.Cadence)
		if err != nil {
			return ErrNotStarted
		}
		r.PeriodStart = start.Unix()
		if err = q.CreatePeriod(ctx, db.CreatePeriodParams{
			ID:          ID("per_"),
			UserID:      u.ID,
			AllowanceID: u.AllowanceID,
			Start:       start.Unix(),
			End:         end.Unix(),
		}); err != nil {
			return err
		}
		period, err := q.GetPeriodByAllowanceStart(ctx, db.GetPeriodByAllowanceStartParams{
			AllowanceID: u.AllowanceID,
			Start:       start.Unix(),
		})
		if err != nil {
			return err
		}
		r.PeriodID = period.ID
		if period.Spent >= u.Budget {
			return ErrBudget
		}
		routeJSON, err := json.Marshal(route)
		if err != nil {
			return err
		}
		providerJSON, err := json.Marshal(provider)
		if err != nil {
			return err
		}
		return q.CreateRequest(ctx, db.CreateRequestParams{
			ID:               r.ID,
			UserID:           r.UserID,
			KeyID:            r.KeyID,
			ModelAccessGroup: r.ModelAccessGroup,
			Model:            r.Model,
			Provider:         r.Provider,
			UpstreamModel:    r.UpstreamModel,
			Route:            string(routeJSON),
			ProviderConfig:   string(providerJSON),
			Created:          r.Created,
			PeriodStart:      r.PeriodStart,
			PeriodID:         r.PeriodID,
			PolicyID:         r.PolicyID,
			AllowanceID:      r.AllowanceID,
		})
	})
	return r, err
}

func (s *Store) Generation(ctx context.Context, id, generation string) error {
	if len(generation) > 512 {
		return errors.New("generation identifier too long")
	}
	return s.q.SetRequestGeneration(ctx, db.SetRequestGenerationParams{GenerationID: generation, ID: id})
}

func (s *Store) Finish(ctx context.Context, id, lifecycle, code string) error {
	now := time.Now().Unix()
	return s.q.FinishRequest(ctx, db.FinishRequestParams{
		Lifecycle:   lifecycle,
		ErrorCode:   code,
		Finished:    &now,
		NextAttempt: time.Now().Add(5 * time.Second).Unix(),
		ID:          id,
	})
}

func (s *Store) ObservedUsage(ctx context.Context, id string, usage json.RawMessage) error {
	if len(usage) == 0 {
		return nil
	}
	return s.q.SetRequestUsage(ctx, db.SetRequestUsageParams{Usage: string(usage), ID: id})
}

func (s *Store) Request(ctx context.Context, id string) (Request, error) {
	row, err := s.q.GetRequest(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Request{}, ErrNotFound
	}
	if err != nil {
		return Request{}, err
	}
	return requestFromDB(row)
}

// Settle atomically inserts an initial charge or appends a manual correction.
// Repeated automatic settlement is a no-op; repeated manual totals produce no delta.
func (s *Store) Settle(ctx context.Context, id string, amount int64, raw string, usage json.RawMessage, source, reason string, manual bool) error {
	if amount < 0 {
		return errors.New("negative total charge")
	}
	if len(usage) == 0 {
		usage = json.RawMessage(`{}`)
	}
	return s.transaction(ctx, func(q *db.Queries) error {
		row, err := q.GetRequest(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		r, err := requestFromDB(row)
		if err != nil {
			return err
		}
		if !manual && r.Accounting == "settled" {
			return nil
		}
		if manual && r.Finished == nil {
			return ErrConflict
		}
		old := int64(0)
		if r.Cost != nil {
			old = *r.Cost
		}
		delta := amount - old
		spent, err := q.GetPeriodSpent(ctx, db.GetPeriodSpentParams{UserID: r.UserID, ID: r.PeriodID})
		if err != nil {
			return err
		}
		if periodTotalOverflows(spent, delta) {
			return errors.New("period total overflow")
		}
		if manual {
			source = "manual_adjustment"
		}
		if err = q.CreateLedgerEntry(ctx, db.CreateLedgerEntryParams{
			RequestID: id,
			Amount:    delta,
			Source:    source,
			Reason:    reason,
			Created:   time.Now().Unix(),
		}); err != nil {
			return err
		}
		if err = q.AddPeriodSpent(ctx, db.AddPeriodSpentParams{Spent: delta, UserID: r.UserID, ID: r.PeriodID}); err != nil {
			return err
		}
		if manual {
			usage = r.Usage
		}
		if err = q.SettleRequest(ctx, db.SettleRequestParams{
			Cost:    &amount,
			RawCost: raw,
			Usage:   string(usage),
			ID:      id,
		}); err != nil {
			return err
		}
		if manual {
			return createAudit(ctx, q, "request.resolve", id, map[string]any{
				"total_usd": FormatUSD(amount),
				"reason":    reason,
			})
		}
		return nil
	})
}

func (s *Store) Summary(ctx context.Context, id string, now time.Time) (Summary, error) {
	var out Summary
	err := s.transaction(ctx, func(q *db.Queries) error {
		u, err := getUser(ctx, q, id)
		if err != nil {
			return err
		}
		out.User = u
		out.Periods = []PeriodUsage{}
		start, end, periodErr := Period(time.Unix(u.Anchor, 0), now, u.Cadence)
		if periodErr == nil {
			out.Current = &PeriodUsage{
				AllowanceID: u.AllowanceID,
				Start:       start.Unix(),
				End:         end.Unix(),
				SpentUSD:    FormatUSD(0),
			}
			period, err := q.GetPeriodByAllowanceStart(ctx, db.GetPeriodByAllowanceStartParams{
				AllowanceID: u.AllowanceID,
				Start:       start.Unix(),
			})
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil {
				out.Current.ID = period.ID
				out.Current.SpentUSD = FormatUSD(period.Spent)
			}
		}
		periods, err := q.ListUserPeriods(ctx, id)
		if err != nil {
			return err
		}
		for _, p := range periods {
			out.Periods = append(out.Periods, PeriodUsage{
				ID:          p.ID,
				AllowanceID: p.AllowanceID,
				Start:       p.Start,
				End:         p.End,
				SpentUSD:    FormatUSD(p.Spent),
			})
		}
		counts, err := q.CountAccountingForUser(ctx, id)
		if err != nil {
			return err
		}
		out.Unresolved = int(counts.Unresolved)
		out.Pending = int(counts.Pending)
		return nil
	})
	if err != nil {
		return Summary{}, err
	}
	return out, nil
}

// History uses an opaque request ID cursor; creation time plus ID gives stable pagination.
func (s *Store) History(ctx context.Context, user, accounting, after string, limit int) ([]Request, error) {
	afterCreated := int64(math.MaxInt64)
	afterID := ""
	if after != "" {
		cursor, err := s.Request(ctx, after)
		if err != nil {
			return nil, err
		}
		afterCreated = cursor.Created
		afterID = cursor.ID
	}
	var (
		rows []db.Request
		err  error
	)
	switch {
	case user != "" && accounting != "":
		rows, err = s.q.ListUserRequestHistoryByAccounting(ctx, db.ListUserRequestHistoryByAccountingParams{
			UserID: user, Accounting: accounting, AfterCreated: afterCreated, AfterID: afterID, Limit: int64(limit),
		})
	case user != "":
		rows, err = s.q.ListUserRequestHistory(ctx, db.ListUserRequestHistoryParams{
			UserID: user, AfterCreated: afterCreated, AfterID: afterID, Limit: int64(limit),
		})
	case accounting != "":
		rows, err = s.q.ListRequestHistoryByAccounting(ctx, db.ListRequestHistoryByAccountingParams{
			Accounting: accounting, AfterCreated: afterCreated, AfterID: afterID, Limit: int64(limit),
		})
	default:
		rows, err = s.q.ListRequestHistory(ctx, db.ListRequestHistoryParams{
			AfterCreated: afterCreated, AfterID: afterID, Limit: int64(limit),
		})
	}
	if err != nil {
		return nil, err
	}
	return requestsFromDB(rows)
}

func (s *Store) Recover(ctx context.Context) error {
	now := time.Now().Unix()
	return s.q.RecoverRequests(ctx, db.RecoverRequestsParams{Finished: &now, NextAttempt: now})
}

func (s *Store) RecoverStale(ctx context.Context, before int64) error {
	now := time.Now().Unix()
	return s.q.RecoverStaleRequests(ctx, db.RecoverStaleRequestsParams{Finished: &now, NextAttempt: now, Created: before})
}

func (s *Store) Pending(ctx context.Context, limit int) ([]Request, error) {
	rows, err := s.q.ListPendingRequests(ctx, db.ListPendingRequestsParams{
		NextAttempt: time.Now().Unix(),
		Limit:       int64(limit),
	})
	if err != nil {
		return nil, err
	}
	return requestsFromDB(rows)
}

func (s *Store) Retry(ctx context.Context, r Request, max int) error {
	attempts, status, nextAttempt := retrySchedule(r.Attempts, max, time.Now())
	return s.q.RetryRequest(ctx, db.RetryRequestParams{
		Attempts:    int64(attempts),
		NextAttempt: nextAttempt,
		Accounting:  status,
		ID:          r.ID,
	})
}

func (s *Store) AccountingCounts(ctx context.Context) (pending, unresolved int, err error) {
	counts, err := s.q.CountAccounting(ctx)
	if err != nil {
		return 0, 0, err
	}
	return int(counts.Pending), int(counts.Unresolved), nil
}

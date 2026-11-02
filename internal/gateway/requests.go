package gateway

import (
	"encoding/json"
	"math"
	"time"
)

type Request struct {
	ModelAccessGroup string          `json:"model_access_group"`
	ID               string          `json:"id"`
	UserID           string          `json:"user_id"`
	KeyID            string          `json:"key_id"`
	Model            string          `json:"model"`
	Provider         string          `json:"provider"`
	UpstreamModel    string          `json:"upstream_model"`
	Route            Route           `json:"-"`
	ProviderConfig   Provider        `json:"-"`
	GenerationID     string          `json:"generation_id"`
	Created          int64           `json:"created_unix"`
	PeriodID         string          `json:"period_id"`
	PolicyID         string          `json:"budget_policy_id"`
	AllowanceID      string          `json:"budget_allowance_id"`
	PeriodStart      int64           `json:"period_start_unix"`
	Lifecycle        string          `json:"lifecycle"`
	Accounting       string          `json:"accounting"`
	Cost             *int64          `json:"-"`
	CostUSD          *string         `json:"cost_usd"`
	RawCost          string          `json:"raw_cost"`
	Usage            json.RawMessage `json:"usage"`
	Attempts         int             `json:"attempts"`
	NextAttempt      int64           `json:"next_attempt_unix"`
	ErrorCode        string          `json:"error_code"`
	Finished         *int64          `json:"finished_unix"`
}

type PeriodUsage struct {
	ID          string `json:"id"`
	AllowanceID string `json:"budget_allowance_id"`
	Start       int64  `json:"start_unix"`
	End         int64  `json:"end_unix"`
	SpentUSD    string `json:"spent_usd"`
}

type Summary struct {
	User       User          `json:"user"`
	Current    *PeriodUsage  `json:"current_period"`
	Unresolved int           `json:"unresolved_requests"`
	Pending    int           `json:"pending_requests"`
	Periods    []PeriodUsage `json:"periods" nullable:"false"`
}

func periodTotalOverflows(spent, delta int64) bool {
	return delta > 0 && spent > math.MaxInt64-delta || delta < 0 && spent < -delta
}

func retrySchedule(current, max int, now time.Time) (attempts int, status string, nextAttempt int64) {
	attempts = current + 1
	status = "pending"
	if attempts >= max {
		status = "unresolved"
	}
	delay := 5 * time.Second * time.Duration(1<<min(attempts, 10))
	if delay > time.Hour {
		delay = time.Hour
	}
	return attempts, status, now.Add(delay).Unix()
}

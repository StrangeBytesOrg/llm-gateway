package gateway

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// BudgetPolicy identifies a recurring allowance. PATCH edits its current limit
// in place; transitions retain old policies and create fresh allowances.
type BudgetPolicy struct {
	ID          string `json:"id"`
	AllowanceID string `json:"budget_allowance_id"`
	UserID      string `json:"user_id"`
	BudgetUSD   string `json:"budget_usd"`
	Cadence     string `json:"cadence"`
	Anchor      int64  `json:"anchor_unix"`
	Created     int64  `json:"created_unix"`
}

type BudgetTransition struct {
	TransitionID     string    `json:"transition_id" minLength:"1" maxLength:"256" doc:"Unique event ID scoped to this user. Retry with the exact same payload."`
	ExpectedPolicyID string    `json:"expected_policy_id" minLength:"1" maxLength:"100" doc:"Current budget_policy_id observed by the caller. Stale transitions return 409."`
	BudgetUSD        string    `json:"budget_usd" minLength:"1" maxLength:"100"`
	Cadence          string    `json:"cadence" enum:"weekly,monthly"`
	Anchor           time.Time `json:"anchor" doc:"UTC recurrence anchor; fractional seconds are truncated. Activation is immediate; a future anchor blocks admission until it starts."`
	ModelAccessGroup *string   `json:"model_access_group,omitempty" minLength:"1" maxLength:"64" doc:"Optional model access assignment committed with the new policy."`
	Enabled          *bool     `json:"enabled,omitempty"`
}

// Match PATCH's assignment semantics: omission preserves the group; explicit
// null is invalid rather than silently treating a malformed assignment as absent.
func (in *BudgetTransition) UnmarshalJSON(data []byte) error {
	type plain BudgetTransition
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if raw, ok := fields["model_access_group"]; ok && strings.TrimSpace(string(raw)) == "null" {
		return errors.New("model_access_group cannot be null")
	}
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*in = BudgetTransition(value)
	return nil
}

func validatePolicy(budget int64, cadence string, anchor time.Time) error {
	anchor = anchor.UTC()
	if budget < 0 || (cadence != "weekly" && cadence != "monthly") || anchor.IsZero() || anchor.Year() < 1970 || anchor.Year() > 9990 {
		return ErrInvalidPolicy
	}
	return nil
}

package gateway

import (
	"context"
	"database/sql"
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

func insertAllowance(ctx context.Context, tx *sql.Tx, u User) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO budget_allowances(id,user_id,cadence,anchor) VALUES(?,?,?,?)", u.AllowanceID, u.ID, u.Cadence, u.Anchor)
	return err
}

func insertPolicy(ctx context.Context, tx *sql.Tx, u User) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO budget_policies(id,user_id,budget,created,allowance_id) VALUES(?,?,?,?,?)", u.PolicyID, u.ID, u.Budget, time.Now().Unix(), u.AllowanceID)
	return err
}

// TransitionBudget checks replay before the current-policy precondition. A replay
// returns the original committed result even if later transitions have occurred.
func (s *Store) TransitionBudget(ctx context.Context, id string, in BudgetTransition, config Config) (User, error) {
	amount, err := ParseUSD(in.BudgetUSD)
	if err != nil {
		return User{}, ErrInvalidPolicy
	}
	if err = validatePolicy(amount, in.Cadence, in.Anchor); err != nil {
		return User{}, err
	}
	if strings.TrimSpace(in.TransitionID) == "" || len(in.TransitionID) > 256 || in.ExpectedPolicyID == "" || len(in.ExpectedPolicyID) > 100 {
		return User{}, ErrInvalidPolicy
	}
	if in.ModelAccessGroup != nil && !groupIDPattern.MatchString(*in.ModelAccessGroup) {
		return User{}, ErrInvalidGroup
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return User{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	var previous, result string
	err = tx.QueryRowContext(ctx, "SELECT input,result FROM budget_transitions WHERE user_id=? AND id=?", id, in.TransitionID).Scan(&previous, &result)
	if err == nil {
		if previous != string(payload) {
			return User{}, ErrTransitionConflict
		}
		var u User
		if err = json.Unmarshal([]byte(result), &u); err != nil {
			return User{}, err
		}
		u.Budget, err = ParseUSD(u.BudgetUSD)
		return u, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return User{}, err
	}
	u, err := scanUser(tx.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	if u.PolicyID != in.ExpectedPolicyID {
		return User{}, ErrStalePolicy
	}
	oldGroup := u.ModelAccessGroup
	u.PolicyID = ID("pol_")
	u.Budget = amount
	u.BudgetUSD = FormatUSD(amount)
	if in.ModelAccessGroup != nil {
		if _, ok := config.ModelAccessGroups[*in.ModelAccessGroup]; !ok {
			return User{}, ErrInvalidGroup
		}
		u.ModelAccessGroup = *in.ModelAccessGroup
	}
	if in.Enabled != nil {
		u.Enabled = *in.Enabled
	}
	u.Cadence = in.Cadence
	u.Anchor = in.Anchor.Unix()
	u.AllowanceID = ID("allow_")
	if err = insertAllowance(ctx, tx, u); err != nil {
		return User{}, err
	}
	if err = insertPolicy(ctx, tx, u); err != nil {
		return User{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE users SET current_policy_id=?,enabled=?,model_access_group=? WHERE id=?", u.PolicyID, u.Enabled, u.ModelAccessGroup, id); err != nil {
		return User{}, err
	}
	resultJSON, err := json.Marshal(u)
	if err != nil {
		return User{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO budget_transitions(user_id,id,policy_id,input,result) VALUES(?,?,?,?,?)", id, in.TransitionID, u.PolicyID, string(payload), string(resultJSON)); err != nil {
		return User{}, err
	}
	if err = audit(ctx, tx, "budget.transition", id, struct {
		User
		OperationID         string `json:"operation_id"`
		PreviousPolicyID    string `json:"previous_policy_id"`
		OldModelAccessGroup string `json:"old_model_access_group"`
	}{u, in.TransitionID, in.ExpectedPolicyID, oldGroup}); err != nil {
		return User{}, err
	}
	if err = tx.Commit(); err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *Store) BudgetPolicies(ctx context.Context, id, after string, limit int) ([]BudgetPolicy, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT p.id,p.user_id,p.budget,a.cadence,a.anchor,p.created,p.allowance_id FROM budget_policies p JOIN budget_allowances a ON a.id=p.allowance_id WHERE p.user_id=? AND p.id>? ORDER BY p.id LIMIT ?", id, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BudgetPolicy{}
	for rows.Next() {
		var p BudgetPolicy
		var amount int64
		if err = rows.Scan(&p.ID, &p.UserID, &amount, &p.Cadence, &p.Anchor, &p.Created, &p.AllowanceID); err != nil {
			return nil, err
		}
		p.BudgetUSD = FormatUSD(amount)
		out = append(out, p)
	}
	return out, rows.Err()
}

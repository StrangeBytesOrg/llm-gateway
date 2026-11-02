package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/StrangeBytesOrg/llm-gateway/internal/gateway/db"
)

func policyFromRow(row db.ListBudgetPoliciesRow) BudgetPolicy {
	return BudgetPolicy{
		ID:          row.ID,
		UserID:      row.UserID,
		BudgetUSD:   FormatUSD(row.Budget),
		Cadence:     row.Cadence,
		Anchor:      row.Anchor,
		Created:     row.Created,
		AllowanceID: row.AllowanceID,
	}
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
	var result User
	err = s.transaction(ctx, func(q *db.Queries) error {
		previous, err := q.GetBudgetTransition(ctx, db.GetBudgetTransitionParams{UserID: id, ID: in.TransitionID})
		if err == nil {
			if previous.Input != string(payload) {
				return ErrTransitionConflict
			}
			var u User
			if err = json.Unmarshal([]byte(previous.Result), &u); err != nil {
				return err
			}
			u.Budget, err = ParseUSD(u.BudgetUSD)
			if err != nil {
				return err
			}
			result = u
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		u, err := getUser(ctx, q, id)
		if err != nil {
			return err
		}
		if u.PolicyID != in.ExpectedPolicyID {
			return ErrStalePolicy
		}
		oldGroup := u.ModelAccessGroup
		u.PolicyID = ID("pol_")
		u.Budget = amount
		u.BudgetUSD = FormatUSD(amount)
		if in.ModelAccessGroup != nil {
			if _, ok := config.ModelAccessGroups[*in.ModelAccessGroup]; !ok {
				return ErrInvalidGroup
			}
			u.ModelAccessGroup = *in.ModelAccessGroup
		}
		if in.Enabled != nil {
			u.Enabled = *in.Enabled
		}
		u.Cadence = in.Cadence
		u.Anchor = in.Anchor.Unix()
		u.AllowanceID = ID("allow_")
		if err = q.CreateAllowance(ctx, db.CreateAllowanceParams{
			ID:      u.AllowanceID,
			UserID:  u.ID,
			Cadence: u.Cadence,
			Anchor:  u.Anchor,
		}); err != nil {
			return err
		}
		if err = q.CreatePolicy(ctx, db.CreatePolicyParams{
			ID:          u.PolicyID,
			UserID:      u.ID,
			Budget:      u.Budget,
			Created:     time.Now().Unix(),
			AllowanceID: u.AllowanceID,
		}); err != nil {
			return err
		}
		if err = q.UpdateUserPolicy(ctx, db.UpdateUserPolicyParams{
			CurrentPolicyID:  u.PolicyID,
			Enabled:          boolInt(u.Enabled),
			ModelAccessGroup: u.ModelAccessGroup,
			ID:               id,
		}); err != nil {
			return err
		}
		resultJSON, err := json.Marshal(u)
		if err != nil {
			return err
		}
		if err = q.CreateBudgetTransition(ctx, db.CreateBudgetTransitionParams{
			UserID:   id,
			ID:       in.TransitionID,
			PolicyID: u.PolicyID,
			Input:    string(payload),
			Result:   string(resultJSON),
		}); err != nil {
			return err
		}
		if err = createAudit(ctx, q, "budget.transition", id, struct {
			User
			OperationID         string `json:"operation_id"`
			PreviousPolicyID    string `json:"previous_policy_id"`
			OldModelAccessGroup string `json:"old_model_access_group"`
		}{u, in.TransitionID, in.ExpectedPolicyID, oldGroup}); err != nil {
			return err
		}
		result = u
		return nil
	})
	if err != nil {
		return User{}, err
	}
	return result, nil
}

func (s *Store) BudgetPolicies(ctx context.Context, id, after string, limit int) ([]BudgetPolicy, error) {
	rows, err := s.q.ListBudgetPolicies(ctx, db.ListBudgetPoliciesParams{UserID: id, ID: after, Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]BudgetPolicy, 0, len(rows))
	for _, row := range rows {
		out = append(out, policyFromRow(row))
	}
	return out, nil
}

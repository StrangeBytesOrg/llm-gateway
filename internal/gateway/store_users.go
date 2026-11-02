package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/StrangeBytesOrg/llm-gateway/internal/gateway/db"
)

func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func userFromRecord(id, alias string, enabled int64, budget int64, cadence string, anchor, created int64, group, policyID, allowanceID string) User {
	return User{
		ID:               id,
		Alias:            alias,
		Enabled:          enabled != 0,
		Budget:           budget,
		BudgetUSD:        FormatUSD(budget),
		Cadence:          cadence,
		Anchor:           anchor,
		Created:          created,
		ModelAccessGroup: group,
		PolicyID:         policyID,
		AllowanceID:      allowanceID,
	}
}

func userFromRow(row db.GetUserRow) User {
	return userFromRecord(row.ID, row.Alias, row.Enabled, row.Budget, row.Cadence, row.Anchor, row.Created, row.ModelAccessGroup, row.CurrentPolicyID, row.AllowanceID)
}

func userFromListRow(row db.ListUsersRow) User {
	return userFromRecord(row.ID, row.Alias, row.Enabled, row.Budget, row.Cadence, row.Anchor, row.Created, row.ModelAccessGroup, row.CurrentPolicyID, row.AllowanceID)
}

func getUser(ctx context.Context, q *db.Queries, id string) (User, error) {
	row, err := q.GetUser(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	return userFromRow(row), nil
}

func (s *Store) CreateUser(ctx context.Context, id string, budget int64, cadence string, anchor time.Time, group string, aliases ...string) (User, error) {
	if err := validatePolicy(budget, cadence, anchor); err != nil {
		return User{}, err
	}
	if !groupIDPattern.MatchString(group) {
		return User{}, ErrInvalidGroup
	}
	if id == "" {
		id = ID("usr_")
	}
	if !validUserID(id) {
		return User{}, ErrInvalidUserID
	}
	alias := ""
	if len(aliases) > 0 {
		alias = aliases[0]
	}
	u := newUser(id, budget, cadence, anchor, group, alias)
	err := s.transaction(ctx, func(q *db.Queries) error {
		exists, err := q.UserExists(ctx, u.ID)
		if err != nil {
			return err
		}
		if exists != 0 {
			return ErrConflict
		}
		if err = q.CreateUser(ctx, db.CreateUserParams{
			ID:               u.ID,
			Enabled:          boolInt(u.Enabled),
			CurrentPolicyID:  u.PolicyID,
			Created:          u.Created,
			ModelAccessGroup: u.ModelAccessGroup,
			Alias:            u.Alias,
		}); err != nil {
			return err
		}
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
			Created:     u.Created,
			AllowanceID: u.AllowanceID,
		}); err != nil {
			return err
		}
		return createAudit(ctx, q, "user.create", u.ID, u)
	})
	if err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *Store) User(ctx context.Context, id string) (User, error) {
	return getUser(ctx, s.q, id)
}

func (s *Store) Users(ctx context.Context, after string, limit int) ([]User, error) {
	rows, err := s.q.ListUsers(ctx, db.ListUsersParams{ID: after, Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]User, 0, len(rows))
	for _, row := range rows {
		out = append(out, userFromListRow(row))
	}
	return out, nil
}

func (s *Store) UpdateUser(ctx context.Context, id string, enabled *bool, budget *int64, group *string, aliases ...*string) (User, error) {
	var updated User
	err := s.transaction(ctx, func(q *db.Queries) error {
		u, err := getUser(ctx, q, id)
		if err != nil {
			return err
		}
		if len(aliases) > 0 && aliases[0] != nil {
			u.Alias = *aliases[0]
		}
		if group != nil {
			if !groupIDPattern.MatchString(*group) {
				return ErrInvalidGroup
			}
			u.ModelAccessGroup = *group
		}
		if enabled != nil {
			u.Enabled = *enabled
		}
		if budget != nil {
			if *budget < 0 {
				return ErrInvalidPolicy
			}
			u.Budget = *budget
			if err = q.UpdatePolicyBudget(ctx, db.UpdatePolicyBudgetParams{Budget: u.Budget, ID: u.PolicyID}); err != nil {
				return err
			}
		}
		u.BudgetUSD = FormatUSD(u.Budget)
		if err = q.UpdateUser(ctx, db.UpdateUserParams{
			Enabled:          boolInt(u.Enabled),
			ModelAccessGroup: u.ModelAccessGroup,
			Alias:            u.Alias,
			ID:               u.ID,
		}); err != nil {
			return err
		}
		updated = u
		return nil
	})
	if err != nil {
		return User{}, err
	}
	return updated, nil
}

// ValidateUserGroups detects removed or renamed groups before serving traffic.
func (s *Store) ValidateUserGroups(ctx context.Context, c Config) error {
	groups, err := s.q.ListUserGroups(ctx)
	if err != nil {
		return err
	}
	for _, group := range groups {
		if _, ok := c.ModelAccessGroups[group]; !ok {
			return fmt.Errorf("stored user references undefined model access group %q", group)
		}
	}
	return nil
}

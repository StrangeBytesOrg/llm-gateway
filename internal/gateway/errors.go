package gateway

import "errors"

var (
	ErrModelNotAllowed    = errors.New("model_not_allowed")
	ErrInvalidGroup       = errors.New("invalid_model_access_group")
	ErrUnauthorized       = errors.New("invalid_api_key")
	ErrDisabled           = errors.New("user_disabled")
	ErrBudget             = errors.New("budget_exhausted")
	ErrNotStarted         = errors.New("budget_not_started")
	ErrInvalidUserID      = errors.New("invalid_user_id")
	ErrInvalidPolicy      = errors.New("invalid_budget_policy")
	ErrStalePolicy        = errors.New("stale_budget_policy")
	ErrTransitionConflict = errors.New("transition_conflict")
	ErrConflict           = errors.New("conflict")
	ErrNotFound           = errors.New("not_found")
	ErrInvalidKeyAlias    = errors.New("invalid_key_alias")
)

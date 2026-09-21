package service

import "errors"

var (
	ErrInvalidTransition = errors.New("requested status transition is not allowed")
	ErrInvalidInput      = errors.New("business input validation failed")
	ErrUnauthorized      = errors.New("invalid username or password")
	ErrInactiveUser      = errors.New("user account is inactive")
	ErrDecisionLocked    = errors.New("final priority decisions are immutable")
	ErrReviewRole        = errors.New("reviewer or admin role is required to finalize a priority")
	ErrSeparationOfDuty  = errors.New("priority preparer cannot approve the same decision")
	ErrNotDecisionOwner  = errors.New("only the preparer may edit this draft decision")
	ErrReviewNotFound    = errors.New("priority review was not found")
	ErrReviewNotPending  = errors.New("priority review has already been processed")
	ErrReviewSoD         = errors.New("reviewer must differ from the preparer of the triggered decision")
	ErrReviewOutcome     = errors.New("review outcome must be maintain, upgrade or release")
	ErrReviewDecision    = errors.New("triggered priority decision is no longer re-reviewable")
)

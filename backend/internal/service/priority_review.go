package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/constants"
	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/dto"
	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/model"
	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/repository"
)

// PriorityReviewService processes the mandatory re-reviews triggered when a
// severe defect is verified against an existing observe/restrict decision.
type PriorityReviewService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.PriorityReview], error)
	Get(context.Context, uint) (model.PriorityReview, error)
	Resolve(context.Context, uint, dto.ResolvePriorityReview, string, string, string) (model.PriorityReview, error)
}

type priorityReviewService struct {
	reviews   repository.PriorityReviewRepository
	decisions repository.PriorityDecisionRepository
	security  SecurityService
}

func NewPriorityReviewService(reviews repository.PriorityReviewRepository, decisions repository.PriorityDecisionRepository, security SecurityService) PriorityReviewService {
	return &priorityReviewService{reviews: reviews, decisions: decisions, security: security}
}

func (s *priorityReviewService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.PriorityReview], error) {
	return s.reviews.List(ctx, query)
}

func (s *priorityReviewService) Get(ctx context.Context, id uint) (model.PriorityReview, error) {
	return s.reviews.Get(ctx, id)
}

func (s *priorityReviewService) Resolve(ctx context.Context, id uint, input dto.ResolvePriorityReview, actor, role, requestID string) (model.PriorityReview, error) {
	review, err := s.reviews.Get(ctx, id)
	if err != nil {
		return model.PriorityReview{}, err
	}
	if review.Status != model.PriorityReviewInitialStatus {
		return model.PriorityReview{}, ErrReviewNotPending
	}
	if role != model.RoleReviewer && role != model.RoleAdmin {
		return model.PriorityReview{}, ErrReviewRole
	}
	// Separation of duty: the re-review handler must differ from the preparer
	// of the triggered priority decision.
	if actor == review.OriginalPreparedBy {
		return model.PriorityReview{}, ErrReviewSoD
	}
	outcome := strings.TrimSpace(input.Outcome)
	basis := strings.TrimSpace(input.ReplacementBasis)
	if basis == "" || len(basis) < 3 {
		return model.PriorityReview{}, ErrInvalidInput
	}
	targetStatus, allowed := map[string]string{
		constants.PriorityReviewOutcomeMaintain: string(constants.PriorityReviewStatusMaintained),
		constants.PriorityReviewOutcomeUpgrade:  string(constants.PriorityReviewStatusUpgraded),
		constants.PriorityReviewOutcomeRelease:  string(constants.PriorityReviewStatusReleased),
	}[outcome]
	if !allowed {
		return model.PriorityReview{}, ErrReviewOutcome
	}

	decision, err := s.decisions.Get(ctx, review.DecisionID)
	if err != nil {
		return model.PriorityReview{}, fmt.Errorf("load triggered decision: %w", err)
	}
	// The original decision must still be in its observe/restrict terminal
	// state; otherwise another resolution has already superseded it.
	if decision.Status != review.OriginalStatus {
		return model.PriorityReview{}, fmt.Errorf("%w: decision is %s", ErrReviewDecision, decision.Status)
	}

	now := time.Now().UTC()
	before := review.Status
	review.Status = targetStatus
	review.Outcome = outcome
	review.ReplacementBasis = basis
	review.ReviewedBy = actor
	review.ReviewRequestID = requestID
	review.ReviewedAt = &now
	review.Version = input.ExpectedVersion + 1
	review.UpdatedAt = now
	review.ResultingDecisionVersion = decision.Version

	var revision *model.PriorityDecisionRevision
	var newDecisionStatus string
	if outcome == constants.PriorityReviewOutcomeUpgrade {
		newDecisionStatus = string(constants.PriorityLevelUrgent)
	} else if outcome == constants.PriorityReviewOutcomeRelease {
		newDecisionStatus = constants.PriorityDecisionReleased
	}
	if newDecisionStatus != "" {
		decision.Status = newDecisionStatus
		decision.Version++
		decision.UpdatedAt = now
		reason := truncateReviewReason(fmt.Sprintf("severe defect re-review %s: %s", outcome, basis))
		built, err := newPriorityRevision(decision, reason, actor, requestID)
		if err != nil {
			return model.PriorityReview{}, err
		}
		revision = &built
		review.ResultingDecisionVersion = decision.Version
	}

	if err := s.reviews.ResolveAndAppendDecision(ctx, &review, input.ExpectedVersion, &decision, decision.Version-1, revision); err != nil {
		if errors.Is(err, repository.ErrVersionConflict) {
			return model.PriorityReview{}, err
		}
		return model.PriorityReview{}, fmt.Errorf("resolve priority review: %w", err)
	}
	if err := s.security.Audit(ctx, actor, requestID, "review_resolve", "PriorityReview", review.ID, before, targetStatus,
		fmt.Sprintf("re-review of %s outcome=%s", review.DecisionCode, outcome)); err != nil {
		return model.PriorityReview{}, fmt.Errorf("persist review audit: %w", err)
	}
	if revision != nil {
		if err := s.security.Audit(ctx, actor, requestID, "transition", "PriorityDecision", decision.ID, review.OriginalStatus, newDecisionStatus,
			fmt.Sprintf("severe defect %s forced re-review outcome=%s", review.DefectCode, outcome)); err != nil {
			return model.PriorityReview{}, fmt.Errorf("persist decision review audit: %w", err)
		}
	}
	return s.reviews.Get(ctx, id)
}

func truncateReviewReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) > 500 {
		return reason[:500]
	}
	return reason
}

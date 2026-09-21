package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/constants"
	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/dto"
	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/model"
	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/repository"
)

type PriorityRecheckService interface {
	List(context.Context, dto.RecheckQuery) (repository.Page[model.PriorityRecheck], error)
	Get(context.Context, uint) (model.PriorityRecheck, error)
	Resolve(context.Context, uint, dto.ResolvePriorityRecheck, string, string, string) (model.PriorityRecheck, error)
	// TriggerForVerifiedDefect is the defect-service hook that generates the
	// pending recheck when a severe defect is verified on a bridge that already
	// carries a terminal observe/restrict decision.
	TriggerForVerifiedDefect(context.Context, model.DefectFinding, string, string) error
}

type priorityRecheckService struct {
	repository repository.PriorityRecheckRepository
	decisions  repository.PriorityDecisionRepository
	security   SecurityService
}

func NewPriorityRecheckService(repo repository.PriorityRecheckRepository, decisions repository.PriorityDecisionRepository, security SecurityService) PriorityRecheckService {
	return &priorityRecheckService{repository: repo, decisions: decisions, security: security}
}

func (s *priorityRecheckService) List(ctx context.Context, query dto.RecheckQuery) (repository.Page[model.PriorityRecheck], error) {
	return s.repository.List(ctx, query)
}

func (s *priorityRecheckService) Get(ctx context.Context, id uint) (model.PriorityRecheck, error) {
	return s.repository.Get(ctx, id)
}

func (s *priorityRecheckService) TriggerForVerifiedDefect(ctx context.Context, defect model.DefectFinding, actor, requestID string) error {
	if defect.Status != string(constants.DefectStateVerified) || defect.RiskLevel != constants.SevereDefectRiskLevel {
		return nil
	}
	item, created, err := s.repository.TriggerForDefect(ctx, defect, requestID)
	if err != nil {
		return fmt.Errorf("trigger 优先级复查: %w", err)
	}
	if !created {
		return nil
	}
	detail := fmt.Sprintf("严重缺陷 %s 核实，同桥终态决定 %s(%s) 触发复查，原决定继续生效", item.DefectCode, item.DecisionCode, item.TriggerLevel)
	_ = s.security.Audit(ctx, actor, requestID, "recheck-trigger", "PriorityRecheck", item.ID, "", model.RecheckStatusPending, detail)
	return nil
}

func (s *priorityRecheckService) Resolve(ctx context.Context, id uint, input dto.ResolvePriorityRecheck, actor, role, requestID string) (model.PriorityRecheck, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.PriorityRecheck{}, err
	}
	if role != model.RoleReviewer && role != model.RoleAdmin {
		return model.PriorityRecheck{}, ErrReviewRole
	}
	if actor == current.DecisionPreparedBy {
		return model.PriorityRecheck{}, ErrRecheckSeparation
	}
	if current.Status != model.RecheckStatusPending {
		return model.PriorityRecheck{}, ErrRecheckClosed
	}
	action := strings.TrimSpace(input.Action)
	status, ok := model.RecheckActionStatus[action]
	if !ok {
		return model.PriorityRecheck{}, ErrInvalidInput
	}
	basis := strings.TrimSpace(input.Basis)
	if len(basis) < 3 {
		return model.PriorityRecheck{}, ErrInvalidInput
	}
	now := time.Now().UTC()
	resolution := model.PriorityRecheck{
		Status: status, Basis: basis, HandledBy: actor,
		HandledAt: &now, RequestID: strings.TrimSpace(requestID), UpdatedAt: now,
	}
	if action == model.RecheckActionEscalate {
		if err := s.escalateDecision(ctx, current, resolution, actor, requestID); err != nil {
			return model.PriorityRecheck{}, err
		}
	} else if err := s.repository.MarkResolved(ctx, id, resolution); err != nil {
		return model.PriorityRecheck{}, fmt.Errorf("resolve 优先级复查: %w", err)
	}
	detail := fmt.Sprintf("复查处理 %s：%s（替代依据留存）", actionLabel(action), basis)
	if err := s.security.Audit(ctx, actor, requestID, "recheck-resolve", "PriorityRecheck", id, model.RecheckStatusPending, status, detail); err != nil {
		return model.PriorityRecheck{}, fmt.Errorf("persist recheck audit: %w", err)
	}
	return s.repository.Get(ctx, id)
}

// escalateDecision moves the triggered decision to urgent with an appended
// immutable revision inside the same transaction as the recheck resolution.
// The repository guards on the expected version and the terminal
// observe/restrict status, so a failure leaves the original decision and
// version untouched.
func (s *priorityRecheckService) escalateDecision(ctx context.Context, recheck model.PriorityRecheck, resolution model.PriorityRecheck, actor, requestID string) error {
	decision, err := s.decisions.Get(ctx, recheck.PriorityDecisionID)
	if err != nil {
		return err
	}
	if decision.Status != string(constants.PriorityLevelObserve) && decision.Status != string(constants.PriorityLevelRestrict) {
		return ErrDecisionLocked
	}
	before := decision.Status
	expectedVersion := decision.Version
	decision.Status = string(constants.PriorityLevelUrgent)
	decision.Version = expectedVersion + 1
	decision.UpdatedAt = resolution.UpdatedAt
	revision, err := newPriorityRevision(decision, resolution.Basis, actor, requestID)
	if err != nil {
		return err
	}
	if err := s.repository.Escalate(ctx, recheck.ID, expectedVersion, &decision, &revision, resolution); err != nil {
		return fmt.Errorf("escalate 优先级决定: %w", err)
	}
	detail := fmt.Sprintf("严重缺陷 %s 复查升级为立即处置：%s", recheck.DefectCode, resolution.Basis)
	if err := s.security.Audit(ctx, actor, requestID, "transition", "PriorityDecision", decision.ID, before, decision.Status, detail); err != nil {
		return fmt.Errorf("persist transition audit: %w", err)
	}
	return nil
}

func actionLabel(action string) string {
	switch action {
	case model.RecheckActionEscalate:
		return "升级为立即处置"
	case model.RecheckActionRelease:
		return "解除"
	default:
		return "维持"
	}
}

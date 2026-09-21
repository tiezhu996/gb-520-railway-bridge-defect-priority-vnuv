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
	"gorm.io/gorm"
)

type DefectFindingService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.DefectFinding], error)
	Get(context.Context, uint) (model.DefectFinding, error)
	Create(context.Context, dto.CreateDefectFinding, string, string) (model.DefectFinding, error)
	Update(context.Context, uint, dto.UpdateDefectFinding, string, string) (model.DefectFinding, error)
	Transition(context.Context, uint, dto.TransitionRequest, string, string) (model.DefectFinding, error)
	Delete(context.Context, uint, string, string) error
	StatusCounts(context.Context) (map[string]int64, error)
}

type defectFindingService struct {
	repository repository.DefectFindingRepository
	reviews    repository.PriorityReviewRepository
	decisions  repository.PriorityDecisionRepository
	security   SecurityService
}

func NewDefectFindingService(repo repository.DefectFindingRepository, reviews repository.PriorityReviewRepository, decisions repository.PriorityDecisionRepository, security SecurityService) DefectFindingService {
	return &defectFindingService{repository: repo, reviews: reviews, decisions: decisions, security: security}
}

func (s *defectFindingService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.DefectFinding], error) {
	page, err := s.repository.List(ctx, query)
	if err != nil {
		return page, err
	}
	ids := make([]uint, 0, len(page.Items))
	for index := range page.Items {
		ids = append(ids, page.Items[index].ID)
	}
	hydrated, err := s.reviews.HydrateByDefectIDs(ctx, ids)
	if err != nil {
		return page, err
	}
	for index := range page.Items {
		page.Items[index].TriggeredReviews = hydrated[page.Items[index].ID]
	}
	return page, nil
}

func (s *defectFindingService) Get(ctx context.Context, id uint) (model.DefectFinding, error) {
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return item, err
	}
	hydrated, err := s.reviews.HydrateByDefectIDs(ctx, []uint{id})
	if err != nil {
		return item, err
	}
	item.TriggeredReviews = hydrated[id]
	return item, nil
}

func (s *defectFindingService) Create(ctx context.Context, input dto.CreateDefectFinding, actor, requestID string) (model.DefectFinding, error) {
	if err := validateDefectFindingBusinessFields(input.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.DefectFinding{}, err
	}
	item := model.DefectFinding{
		BaseModel: model.BaseModel{
			Code: strings.ToUpper(strings.TrimSpace(input.Code)), Name: strings.TrimSpace(input.Name),
			Status: model.DefectFindingInitialStatus, Version: 1, Description: strings.TrimSpace(input.Description),
		},
		Facility: strings.TrimSpace(input.Facility), Owner: strings.TrimSpace(input.Owner),
		Category: strings.TrimSpace(input.Category), RiskLevel: input.RiskLevel,
		MetricValue: input.MetricValue, MetricUnit: strings.TrimSpace(input.MetricUnit),
		EffectiveAt: input.EffectiveAt.UTC(), Evidence: strings.TrimSpace(input.Evidence),
		RelatedCode: strings.ToUpper(strings.TrimSpace(input.RelatedCode)),
	}
	if err := s.repository.Create(ctx, &item); err != nil {
		return model.DefectFinding{}, fmt.Errorf("create 缺陷发现: %w", err)
	}
	_ = s.security.Audit(ctx, actor, requestID, "create", "DefectFinding", item.ID, "", item.Status, "created 缺陷发现")
	return item, nil
}

func (s *defectFindingService) Update(ctx context.Context, id uint, input dto.UpdateDefectFinding, actor, requestID string) (model.DefectFinding, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.DefectFinding{}, err
	}
	if err := validateDefectFindingBusinessFields(current.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.DefectFinding{}, err
	}
	current.Name = strings.TrimSpace(input.Name)
	current.Description = strings.TrimSpace(input.Description)
	current.Facility = strings.TrimSpace(input.Facility)
	current.Owner = strings.TrimSpace(input.Owner)
	current.Category = strings.TrimSpace(input.Category)
	current.RiskLevel = input.RiskLevel
	current.MetricValue = input.MetricValue
	current.MetricUnit = strings.TrimSpace(input.MetricUnit)
	current.EffectiveAt = input.EffectiveAt.UTC()
	current.Evidence = strings.TrimSpace(input.Evidence)
	current.RelatedCode = strings.ToUpper(strings.TrimSpace(input.RelatedCode))
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.repository.Update(ctx, id, input.ExpectedVersion, &current); err != nil {
		return model.DefectFinding{}, fmt.Errorf("update 缺陷发现: %w", err)
	}
	_ = s.security.Audit(ctx, actor, requestID, "update", "DefectFinding", id, current.Status, current.Status, "updated business fields")
	return s.Get(ctx, id)
}

func (s *defectFindingService) Transition(ctx context.Context, id uint, input dto.TransitionRequest, actor, requestID string) (model.DefectFinding, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.DefectFinding{}, err
	}
	target := strings.TrimSpace(input.Status)
	if !constants.CanTransition(constants.DefectFindingTransitions, current.Status, target) {
		return model.DefectFinding{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.Status, target)
	}
	before := current.Status
	now := time.Now().UTC()
	current.Status = target
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = now

	var triggered *model.PriorityReview
	if target == string(constants.DefectStateVerified) && constants.SevereRiskLevels[current.RiskLevel] {
		triggered, err = s.buildReviewIfTriggered(ctx, current, actor, requestID, now)
		if err != nil {
			return model.DefectFinding{}, err
		}
	}

	if triggered != nil {
		if err := s.reviews.UpdateDefectAndCreateReview(ctx, id, input.ExpectedVersion, &current, triggered); err != nil {
			if errors.Is(err, repository.ErrVersionConflict) {
				return model.DefectFinding{}, err
			}
			return model.DefectFinding{}, fmt.Errorf("verify severe defect with priority review: %w", err)
		}
	} else if err := s.repository.Update(ctx, id, input.ExpectedVersion, &current); err != nil {
		return model.DefectFinding{}, fmt.Errorf("transition 缺陷发现: %w", err)
	}

	if err := s.security.Audit(ctx, actor, requestID, "transition", "DefectFinding", id, before, target, input.Reason); err != nil {
		return model.DefectFinding{}, fmt.Errorf("persist transition audit: %w", err)
	}
	if triggered != nil {
		_ = s.security.Audit(ctx, "system", requestID, "review_trigger", "PriorityReview", triggered.ID, "", model.PriorityReviewInitialStatus,
			fmt.Sprintf("severe defect %s verified against decision %s", triggered.DefectCode, triggered.DecisionCode))
	}
	return s.Get(ctx, id)
}

// buildReviewIfTriggered returns a pending review when the same bridge already
// has an observe/restrict terminal decision and no review exists for this
// defect. It returns (nil, nil) when no review should be generated.
func (s *defectFindingService) buildReviewIfTriggered(ctx context.Context, defect model.DefectFinding, actor, requestID string, now time.Time) (*model.PriorityReview, error) {
	if _, err := s.reviews.FindOpenByDefectID(ctx, defect.ID); err == nil {
		// A pending review already exists; concurrent verification must not
		// generate another one.
		return nil, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("check existing priority review: %w", err)
	}
	decision, found, err := s.decisions.FindTriggerForFacility(ctx, defect.Facility,
		[]string{string(constants.PriorityLevelObserve), string(constants.PriorityLevelRestrict)})
	if err != nil {
		return nil, fmt.Errorf("locate triggered priority decision: %w", err)
	}
	if !found {
		return nil, nil
	}
	code := reviewCode(defect.Code)
	review := model.PriorityReview{
		BaseModel: model.BaseModel{
			Code: code,
			Name: fmt.Sprintf("严重缺陷复查 %s", defect.Code),
			Status: model.PriorityReviewInitialStatus, Version: 1,
			Description: fmt.Sprintf("严重缺陷 %s 已核实，同桥决定 %s（%s）触发优先级复查，原决定继续生效",
				defect.Code, decision.Code, decision.Status),
		},
		DefectID: defect.ID, DefectCode: defect.Code, Facility: defect.Facility,
		TriggeredBy: actor, TriggerRequestID: requestID, TriggeredAt: now,
		DecisionID: decision.ID, DecisionCode: decision.Code,
		OriginalStatus: decision.Status, OriginalPreparedBy: decision.PreparedBy,
	}
	return &review, nil
}

// reviewCode keeps the unique PR-<defect> identifier within the code column.
func reviewCode(defectCode string) string {
	prefix := "PR-"
	code := prefix + strings.ToUpper(strings.TrimSpace(defectCode))
	if len(code) > 64 {
		return code[:64]
	}
	return code
}

func (s *defectFindingService) Delete(ctx context.Context, id uint, actor, requestID string) error {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.repository.Delete(ctx, id); err != nil {
		return err
	}
	return s.security.Audit(ctx, actor, requestID, "delete", "DefectFinding", id, current.Status, "deleted", "soft deleted 缺陷发现")
}

func (s *defectFindingService) StatusCounts(ctx context.Context) (map[string]int64, error) {
	return s.repository.CountByStatus(ctx)
}

func validateDefectFindingBusinessFields(code, name, facility, owner string) error {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(facility) == "" || strings.TrimSpace(owner) == "" {
		return ErrInvalidInput
	}
	return nil
}

package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/constants"
	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/dto"
	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PriorityRecheckRepository owns all persistence operations for 严重缺陷触发的优先级复查.
type PriorityRecheckRepository interface {
	List(context.Context, dto.RecheckQuery) (Page[model.PriorityRecheck], error)
	Get(context.Context, uint) (model.PriorityRecheck, error)
	// TriggerForDefect inserts one pending recheck for the newest terminal
	// observe/restrict decision on the defect's facility. The unique defect
	// index makes concurrent triggers collapse into a single row; created is
	// false when no decision matches or the recheck already exists.
	TriggerForDefect(context.Context, model.DefectFinding, string) (model.PriorityRecheck, bool, error)
	// MarkResolved handles maintain/release: only the recheck row changes and
	// the original decision stays untouched.
	MarkResolved(context.Context, uint, model.PriorityRecheck) error
	// Escalate atomically bumps the decision to urgent with a new revision and
	// marks the recheck resolved. Any failure rolls the transaction back so the
	// original decision and version are never overwritten.
	Escalate(context.Context, uint, uint, *model.PriorityDecision, *model.PriorityDecisionRevision, model.PriorityRecheck) error
}

type priorityRecheckRepository struct {
	db *gorm.DB
}

func NewPriorityRecheckRepository(db *gorm.DB) PriorityRecheckRepository {
	return &priorityRecheckRepository{db: db}
}

func (r *priorityRecheckRepository) List(ctx context.Context, q dto.RecheckQuery) (Page[model.PriorityRecheck], error) {
	page, pageSize := normalizePage(q.Page, q.PageSize)
	db := r.db.WithContext(ctx).Model(&model.PriorityRecheck{})
	if q.DefectID > 0 {
		db = db.Where("defect_finding_id = ?", q.DefectID)
	}
	if q.DecisionID > 0 {
		db = db.Where("priority_decision_id = ?", q.DecisionID)
	}
	if status := strings.TrimSpace(q.Status); status != "" {
		db = db.Where("status = ?", status)
	}
	if search := strings.TrimSpace(strings.ToLower(q.Search)); search != "" {
		wildcard := "%" + search + "%"
		db = db.Where("LOWER(defect_code) LIKE ? OR LOWER(decision_code) LIKE ?", wildcard, wildcard)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return Page[model.PriorityRecheck]{}, err
	}
	items := make([]model.PriorityRecheck, 0)
	err := db.Order("created_at DESC, id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	return Page[model.PriorityRecheck]{Items: items, Total: total, Page: page, PageSize: pageSize}, err
}

func (r *priorityRecheckRepository) Get(ctx context.Context, id uint) (model.PriorityRecheck, error) {
	var item model.PriorityRecheck
	err := r.db.WithContext(ctx).First(&item, id).Error
	return item, err
}

func (r *priorityRecheckRepository) TriggerForDefect(ctx context.Context, defect model.DefectFinding, requestID string) (model.PriorityRecheck, bool, error) {
	var decision model.PriorityDecision
	err := r.db.WithContext(ctx).
		Where("facility = ? AND status IN ?", defect.Facility, []string{string(constants.PriorityLevelObserve), string(constants.PriorityLevelRestrict)}).
		Order("updated_at DESC, id DESC").First(&decision).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.PriorityRecheck{}, false, nil
	}
	if err != nil {
		return model.PriorityRecheck{}, false, err
	}
	now := time.Now().UTC()
	item := model.PriorityRecheck{
		DefectFindingID: defect.ID, DefectCode: defect.Code,
		PriorityDecisionID: decision.ID, DecisionCode: decision.Code,
		DecisionPreparedBy: decision.PreparedBy, TriggerLevel: decision.Status,
		Status: model.RecheckStatusPending, RequestID: strings.TrimSpace(requestID),
		CreatedAt: now, UpdatedAt: now,
	}
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&item)
	if result.Error != nil {
		return model.PriorityRecheck{}, false, result.Error
	}
	if result.RowsAffected == 0 {
		return model.PriorityRecheck{}, false, nil
	}
	return item, true, nil
}

func (r *priorityRecheckRepository) MarkResolved(ctx context.Context, id uint, resolution model.PriorityRecheck) error {
	result := r.db.WithContext(ctx).Model(&model.PriorityRecheck{}).
		Where("id = ? AND status = ?", id, model.RecheckStatusPending).
		Updates(map[string]any{
			"status": resolution.Status, "basis": resolution.Basis,
			"handled_by": resolution.HandledBy, "handled_at": resolution.HandledAt,
			"request_id": resolution.RequestID, "updated_at": resolution.UpdatedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrVersionConflict
	}
	return nil
}

func (r *priorityRecheckRepository) Escalate(ctx context.Context, recheckID, expectedDecisionVersion uint, decision *model.PriorityDecision, revision *model.PriorityDecisionRevision, resolution model.PriorityRecheck) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.PriorityDecision{}).
			Where("id = ? AND version = ? AND status IN ?", decision.ID, expectedDecisionVersion,
				[]string{string(constants.PriorityLevelObserve), string(constants.PriorityLevelRestrict)}).
			Select("*").Omit("ID", "Code", "CreatedAt", "DeletedAt", "PreparedBy", "Revisions").
			Updates(decision)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrVersionConflict
		}
		revision.PriorityDecisionID = decision.ID
		if err := tx.Create(revision).Error; err != nil {
			return err
		}
		patch := tx.Model(&model.PriorityRecheck{}).
			Where("id = ? AND status = ?", recheckID, model.RecheckStatusPending).
			Updates(map[string]any{
				"status": resolution.Status, "basis": resolution.Basis,
				"handled_by": resolution.HandledBy, "handled_at": resolution.HandledAt,
				"request_id": resolution.RequestID, "updated_at": resolution.UpdatedAt,
			})
		if patch.Error != nil {
			return patch.Error
		}
		if patch.RowsAffected == 0 {
			return ErrVersionConflict
		}
		return nil
	})
}

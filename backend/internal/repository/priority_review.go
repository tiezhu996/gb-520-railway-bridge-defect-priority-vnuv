package repository

import (
	"context"
	"strings"

	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/dto"
	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PriorityReviewRepository owns persistence for 严重缺陷触发优先级复查. All
// state-changing operations run inside a transaction so a failed review can
// never leave the original decision or its revisions partially overwritten.
type PriorityReviewRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.PriorityReview], error)
	Get(context.Context, uint) (model.PriorityReview, error)
	// UpdateDefectAndCreateReview verifies the defect with optimistic locking and
	// generates the review in one transaction. Either both are committed or
	// neither is.
	UpdateDefectAndCreateReview(ctx context.Context, defectID, expectedDefectVersion uint, defect *model.DefectFinding, review *model.PriorityReview) error
	// FindOpenByDefectID reports whether an existing (non-terminal) review is
	// already attached to the defect.
	FindOpenByDefectID(ctx context.Context, defectID uint) (model.PriorityReview, error)
	// HydrateByDefectIDs returns all reviews keyed by defect ID for list pages.
	HydrateByDefectIDs(ctx context.Context, defectIDs []uint) (map[uint][]model.PriorityReview, error)
	// ResolveAndAppendDecision finalizes a review and, for upgrade/release,
	// appends an immutable decision revision in one transaction. RowsAffected==0
	// on either optimistic lock rolls the whole transaction back.
	ResolveAndAppendDecision(ctx context.Context, review *model.PriorityReview, expectedReviewVersion uint,
		decision *model.PriorityDecision, expectedDecisionVersion uint, revision *model.PriorityDecisionRevision) error
}

type priorityReviewRepository struct {
	db *gorm.DB
}

func NewPriorityReviewRepository(db *gorm.DB) PriorityReviewRepository {
	return &priorityReviewRepository{db: db}
}

func (r *priorityReviewRepository) List(ctx context.Context, q dto.PageQuery) (Page[model.PriorityReview], error) {
	page, pageSize := normalizePage(q.Page, q.PageSize)
	db := r.db.WithContext(ctx).Model(&model.PriorityReview{})
	if search := strings.TrimSpace(strings.ToLower(q.Search)); search != "" {
		wildcard := "%" + search + "%"
		db = db.Where("LOWER(code) LIKE ? OR LOWER(name) LIKE ? OR LOWER(defect_code) LIKE ? OR LOWER(decision_code) LIKE ?", wildcard, wildcard, wildcard, wildcard)
	}
	if status := strings.TrimSpace(q.Status); status != "" {
		db = db.Where("status = ?", status)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return Page[model.PriorityReview]{}, err
	}
	items := make([]model.PriorityReview, 0)
	err := db.Order("updated_at DESC, id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	return Page[model.PriorityReview]{Items: items, Total: total, Page: page, PageSize: pageSize}, err
}

func (r *priorityReviewRepository) Get(ctx context.Context, id uint) (model.PriorityReview, error) {
	var item model.PriorityReview
	err := r.db.WithContext(ctx).First(&item, id).Error
	return item, err
}

func (r *priorityReviewRepository) UpdateDefectAndCreateReview(ctx context.Context, defectID, expectedDefectVersion uint, defect *model.DefectFinding, review *model.PriorityReview) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.DefectFinding{}).
			Where("id = ? AND version = ?", defectID, expectedDefectVersion).
			Select("*").Omit("id", "code", "created_at", "deleted_at").
			Updates(defect)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrVersionConflict
		}
		return tx.Create(review).Error
	})
}

func (r *priorityReviewRepository) FindOpenByDefectID(ctx context.Context, defectID uint) (model.PriorityReview, error) {
	var review model.PriorityReview
	err := r.db.WithContext(ctx).
		Where("defect_id = ? AND status = ?", defectID, model.PriorityReviewInitialStatus).
		First(&review).Error
	return review, err
}

func (r *priorityReviewRepository) HydrateByDefectIDs(ctx context.Context, defectIDs []uint) (map[uint][]model.PriorityReview, error) {
	result := make(map[uint][]model.PriorityReview)
	if len(defectIDs) == 0 {
		return result, nil
	}
	var reviews []model.PriorityReview
	if err := r.db.WithContext(ctx).Where("defect_id IN ?", defectIDs).
		Order("created_at DESC").Find(&reviews).Error; err != nil {
		return nil, err
	}
	for index := range reviews {
		review := reviews[index]
		result[review.DefectID] = append(result[review.DefectID], review)
	}
	return result, nil
}

func (r *priorityReviewRepository) ResolveAndAppendDecision(ctx context.Context, review *model.PriorityReview, expectedReviewVersion uint,
	decision *model.PriorityDecision, expectedDecisionVersion uint, revision *model.PriorityDecisionRevision) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Lock the review row first so concurrent resolutions serialize.
		var locked model.PriorityReview
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&locked, review.ID).Error; err != nil {
			return err
		}
		reviewResult := tx.Model(&model.PriorityReview{}).
			Where("id = ? AND version = ? AND status = ?", review.ID, expectedReviewVersion, model.PriorityReviewInitialStatus).
			Select("*").Omit("id", "code", "created_at", "deleted_at").
			Updates(review)
		if reviewResult.Error != nil {
			return reviewResult.Error
		}
		if reviewResult.RowsAffected == 0 {
			return ErrVersionConflict
		}
		// maintain leaves the original decision and its versions untouched.
		if revision == nil {
			return nil
		}
		decisionResult := tx.Model(&model.PriorityDecision{}).
			Where("id = ? AND version = ?", decision.ID, expectedDecisionVersion).
			Select("*").Omit("id", "code", "created_at", "deleted_at", "prepared_by", "revisions").
			Updates(decision)
		if decisionResult.Error != nil {
			return decisionResult.Error
		}
		if decisionResult.RowsAffected == 0 {
			return ErrVersionConflict
		}
		revision.PriorityDecisionID = decision.ID
		return tx.Create(revision).Error
	})
}

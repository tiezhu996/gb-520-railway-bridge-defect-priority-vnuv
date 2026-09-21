package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/config"
	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/dto"
	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/model"
	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type reviewTestEnv struct {
	db           *gorm.DB
	defects      DefectFindingService
	priorities   PriorityDecisionService
	reviews      PriorityReviewService
	reviewRepo   repository.PriorityReviewRepository
	priorityRepo repository.PriorityDecisionRepository
}

func newReviewTestEnv(t *testing.T) reviewTestEnv {
	t.Helper()
	dsn := fmt.Sprintf("file:review-%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.PriorityDecision{}, &model.PriorityDecisionRevision{},
		&model.DefectFinding{}, &model.PriorityReview{}, &model.AuditLog{},
	); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	security := NewSecurityService(repository.NewSecurityRepository(db), config.Config{})
	priorityRepo := repository.NewPriorityDecisionRepository(db)
	reviewRepo := repository.NewPriorityReviewRepository(db)
	defectRepo := repository.NewDefectFindingRepository(db)
	return reviewTestEnv{
		db:           db,
		defects:      NewDefectFindingService(defectRepo, reviewRepo, priorityRepo, security),
		priorities:   NewPriorityDecisionService(priorityRepo, security),
		reviews:      NewPriorityReviewService(reviewRepo, priorityRepo, security),
		reviewRepo:   reviewRepo,
		priorityRepo: priorityRepo,
	}
}

func TestSevereDefectVerificationTriggersSingleReview(t *testing.T) {
	env := newReviewTestEnv(t)
	ctx := context.Background()
	facility := "K42 桥梁作业区"

	// An operator prepares an observe decision; an independent reviewer finalizes it.
	created, err := env.priorities.Create(ctx, priorityCreateInputAt("PD-RV1", facility, "observe-evidence"), "operator", "req-pd-create")
	if err != nil {
		t.Fatalf("create decision: %v", err)
	}
	if _, err := env.priorities.Transition(ctx, created.ID,
		dto.TransitionRequest{Status: "observe", ExpectedVersion: 1, Reason: "independent observe finalization"},
		"reviewer", model.RoleReviewer, "req-pd-final"); err != nil {
		t.Fatalf("finalize observe: %v", err)
	}

	// A critical defect on the same bridge is created and verified.
	defect, err := env.defects.Create(ctx, criticalDefectInput("DF-RV1", facility), "operator", "req-df-create")
	if err != nil {
		t.Fatalf("create defect: %v", err)
	}
	verified, err := env.defects.Transition(ctx, defect.ID,
		dto.TransitionRequest{Status: "verified", ExpectedVersion: 1, Reason: "severe defect confirmed on site"},
		"operator", "req-df-verify")
	if err != nil {
		t.Fatalf("verify defect: %v", err)
	}
	if verified.Status != "verified" {
		t.Fatalf("defect should be verified, got %s", verified.Status)
	}
	if len(verified.TriggeredReviews) != 1 {
		t.Fatalf("expected exactly one triggered review, got %d", len(verified.TriggeredReviews))
	}
	review := verified.TriggeredReviews[0]
	if review.Status != "pending" || review.OriginalStatus != "observe" || review.DecisionCode != "PD-RV1" ||
		review.OriginalPreparedBy != "operator" || review.DefectCode != "DF-RV1" {
		t.Fatalf("unexpected triggered review: %+v", review)
	}

	// The original decision stays in force: status and version unchanged.
	decision, err := env.priorities.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("reload decision: %v", err)
	}
	if decision.Status != "observe" || decision.Version != 2 || len(decision.Revisions) != 2 {
		t.Fatalf("original decision must remain in force, got status=%s version=%d revisions=%d",
			decision.Status, decision.Version, len(decision.Revisions))
	}

	// Concurrent verification requests (both reading v1/new) must commit only
	// one defect update and one review thanks to the optimistic lock and the
	// unique defect_id index.
	concurrentDefect, err := env.defects.Create(ctx, criticalDefectInput("DF-RV1C", facility), "operator", "req-df-concurrent-create")
	if err != nil {
		t.Fatalf("create concurrent defect: %v", err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	attempt := func(suffix string) {
		<-start
		updated, loadErr := env.defects.Get(ctx, concurrentDefect.ID)
		if loadErr != nil {
			results <- loadErr
			return
		}
		updated.Status = "verified"
		updated.Version = 2
		updated.UpdatedAt = time.Now().UTC()
		updated.TriggeredReviews = nil
		review := &model.PriorityReview{
			BaseModel: model.BaseModel{
				Code: fmt.Sprintf("PR-DF-RV1C-%s", suffix), Name: "并发复查", Status: "pending", Version: 1,
			},
			DefectID: concurrentDefect.ID, DefectCode: concurrentDefect.Code, Facility: facility,
			TriggeredBy: "operator", TriggerRequestID: fmt.Sprintf("req-concurrent-%s", suffix), TriggeredAt: time.Now().UTC(),
			DecisionID: created.ID, DecisionCode: "PD-RV1",
			OriginalStatus: "observe", OriginalPreparedBy: "operator",
		}
		results <- env.reviewRepo.UpdateDefectAndCreateReview(ctx, concurrentDefect.ID, 1, &updated, review)
	}
	go attempt("A")
	go attempt("B")
	close(start)
	successes, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		switch resultErr := <-results; {
		case resultErr == nil:
			successes++
		case errors.Is(resultErr, repository.ErrVersionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent result: %v", resultErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("expected one success and one optimistic-lock conflict, got success=%d conflict=%d", successes, conflicts)
	}
	var concurrentReviews int64
	if err := env.db.Model(&model.PriorityReview{}).Where("defect_id = ?", concurrentDefect.ID).Count(&concurrentReviews).Error; err != nil {
		t.Fatalf("count concurrent reviews: %v", err)
	}
	if concurrentReviews != 1 {
		t.Fatalf("same defect must produce one review only, got %d", concurrentReviews)
	}
	var storedDefect model.DefectFinding
	if err := env.db.First(&storedDefect, concurrentDefect.ID).Error; err != nil {
		t.Fatalf("reload concurrent defect: %v", err)
	}
	if storedDefect.Status != "verified" || storedDefect.Version != 2 {
		t.Fatalf("concurrent defect should be verified v2, got %s v%d", storedDefect.Status, storedDefect.Version)
	}
}

func TestNonSevereOrDifferentFacilityDefectDoesNotTrigger(t *testing.T) {
	env := newReviewTestEnv(t)
	ctx := context.Background()
	facility := "K42 桥梁作业区"

	created, err := env.priorities.Create(ctx, priorityCreateInputAt("PD-RV2", facility, "observe-evidence"), "operator", "req-pd-create")
	if err != nil {
		t.Fatalf("create decision: %v", err)
	}
	if _, err := env.priorities.Transition(ctx, created.ID,
		dto.TransitionRequest{Status: "restrict", ExpectedVersion: 1, Reason: "independent restrict finalization"},
		"reviewer", model.RoleReviewer, "req-pd-final"); err != nil {
		t.Fatalf("finalize restrict: %v", err)
	}

	// Medium defect on same bridge: not severe, no review.
	medium := criticalDefectInput("DF-RV2-M", facility)
	medium.RiskLevel = "medium"
	defect, err := env.defects.Create(ctx, medium, "operator", "req-df-create")
	if err != nil {
		t.Fatalf("create medium defect: %v", err)
	}
	verified, err := env.defects.Transition(ctx, defect.ID,
		dto.TransitionRequest{Status: "verified", ExpectedVersion: 1, Reason: "medium defect confirmed"},
		"operator", "req-df-verify")
	if err != nil {
		t.Fatalf("verify medium defect: %v", err)
	}
	if len(verified.TriggeredReviews) != 0 {
		t.Fatalf("medium defect must not trigger a review: %+v", verified.TriggeredReviews)
	}

	// Critical defect on another bridge: no same-bridge decision, no review.
	other := criticalDefectInput("DF-RV2-X", "其他桥梁 K99")
	otherDefect, err := env.defects.Create(ctx, other, "operator", "req-df-create-2")
	if err != nil {
		t.Fatalf("create other defect: %v", err)
	}
	verifiedOther, err := env.defects.Transition(ctx, otherDefect.ID,
		dto.TransitionRequest{Status: "verified", ExpectedVersion: 1, Reason: "critical defect elsewhere"},
		"operator", "req-df-verify-2")
	if err != nil {
		t.Fatalf("verify other defect: %v", err)
	}
	if len(verifiedOther.TriggeredReviews) != 0 {
		t.Fatalf("defect on another bridge must not trigger a review: %+v", verifiedOther.TriggeredReviews)
	}
	page, err := env.reviews.List(ctx, dto.PageQuery{Page: 1, PageSize: 50})
	if err != nil {
		t.Fatalf("list reviews: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("expected no reviews, got %d", page.Total)
	}
}

func TestReviewResolutionMaintainUpgradeReleaseAndGuards(t *testing.T) {
	ctx := context.Background()

	// --- maintain keeps the decision untouched ---
	env := newReviewTestEnv(t)
	review := seedPendingReview(t, env, "PD-RVM", "DF-RVM", "observe")
	maintained, err := env.reviews.Resolve(ctx, review.ID,
		dto.ResolvePriorityReview{ExpectedVersion: 1, Outcome: "maintain", ReplacementBasis: "复测数据维持观察决定"},
		"reviewer", model.RoleReviewer, "req-maintain")
	if err != nil {
		t.Fatalf("maintain resolution: %v", err)
	}
	if maintained.Status != "maintained" || maintained.Outcome != "maintain" || maintained.ReviewedBy != "reviewer" ||
		maintained.ResultingDecisionVersion != 2 {
		t.Fatalf("unexpected maintained review: %+v", maintained)
	}
	decision, err := env.priorities.Get(ctx, review.DecisionID)
	if err != nil {
		t.Fatalf("reload decision: %v", err)
	}
	if decision.Status != "observe" || decision.Version != 2 || len(decision.Revisions) != 2 {
		t.Fatalf("maintain must leave decision untouched, got status=%s version=%d revisions=%d",
			decision.Status, decision.Version, len(decision.Revisions))
	}
	// Reprocessing a processed review fails and changes nothing.
	if _, err := env.reviews.Resolve(ctx, review.ID,
		dto.ResolvePriorityReview{ExpectedVersion: maintained.Version, Outcome: "upgrade", ReplacementBasis: "late change"},
		"reviewer", model.RoleReviewer, "req-again"); !errors.Is(err, ErrReviewNotPending) {
		t.Fatalf("processed review must reject re-resolution, got %v", err)
	}

	// --- upgrade appends an urgent revision ---
	env = newReviewTestEnv(t)
	review = seedPendingReview(t, env, "PD-RVU", "DF-RVU", "restrict")
	upgraded, err := env.reviews.Resolve(ctx, review.ID,
		dto.ResolvePriorityReview{ExpectedVersion: 1, Outcome: "upgrade", ReplacementBasis: "裂缝扩展需立即处置"},
		"admin", model.RoleAdmin, "req-upgrade")
	if err != nil {
		t.Fatalf("upgrade resolution: %v", err)
	}
	if upgraded.Status != "upgraded" || upgraded.ResultingDecisionVersion != 3 {
		t.Fatalf("unexpected upgraded review: %+v", upgraded)
	}
	decision, err = env.priorities.Get(ctx, review.DecisionID)
	if err != nil {
		t.Fatalf("reload decision: %v", err)
	}
	if decision.Status != "urgent" || decision.Version != 3 || len(decision.Revisions) != 3 {
		t.Fatalf("upgrade must append urgent v3, got status=%s version=%d revisions=%d",
			decision.Status, decision.Version, len(decision.Revisions))
	}
	last := decision.Revisions[2]
	if last.Status != "urgent" || last.Actor != "admin" || last.RequestID != "req-upgrade" ||
		!strings.Contains(last.Reason, "裂缝扩展需立即处置") {
		t.Fatalf("upgrade revision lost replacement basis: %+v", last)
	}
	// Original v1/v2 evidence, actors and requests remain intact.
	if decision.Revisions[0].Actor != "operator" || decision.Revisions[1].Status != "restrict" ||
		decision.Revisions[0].RequestID != "seed-pd-create" || decision.Revisions[1].RequestID != "seed-pd-final" {
		t.Fatalf("original revisions must be preserved: %+v", decision.Revisions)
	}

	// --- release appends a released revision ---
	env = newReviewTestEnv(t)
	review = seedPendingReview(t, env, "PD-RVR", "DF-RVR", "observe")
	released, err := env.reviews.Resolve(ctx, review.ID,
		dto.ResolvePriorityReview{ExpectedVersion: 1, Outcome: "release", ReplacementBasis: "缺陷已消除，解除观察"},
		"reviewer", model.RoleReviewer, "req-release")
	if err != nil {
		t.Fatalf("release resolution: %v", err)
	}
	if released.Status != "released" || released.ResultingDecisionVersion != 3 {
		t.Fatalf("unexpected released review: %+v", released)
	}
	decision, err = env.priorities.Get(ctx, review.DecisionID)
	if err != nil {
		t.Fatalf("reload decision: %v", err)
	}
	if decision.Status != "released" || decision.Version != 3 || len(decision.Revisions) != 3 ||
		decision.Revisions[2].Status != "released" {
		t.Fatalf("release must append released v3, got status=%s version=%d revisions=%d",
			decision.Status, decision.Version, len(decision.Revisions))
	}

	// --- guards ---
	env = newReviewTestEnv(t)
	review = seedPendingReview(t, env, "PD-RVG", "DF-RVG", "restrict")
	// Operator cannot resolve.
	if _, err := env.reviews.Resolve(ctx, review.ID,
		dto.ResolvePriorityReview{ExpectedVersion: 1, Outcome: "maintain", ReplacementBasis: "无权重试"},
		"operator", model.RoleOperator, "req-role"); !errors.Is(err, ErrReviewRole) {
		t.Fatalf("operator resolution should be forbidden, got %v", err)
	}
	// The original preparer (operator) cannot resolve even with a reviewer name.
	if _, err := env.reviews.Resolve(ctx, review.ID,
		dto.ResolvePriorityReview{ExpectedVersion: 1, Outcome: "maintain", ReplacementBasis: "原拟制人重审"},
		"operator", model.RoleAdmin, "req-sod"); !errors.Is(err, ErrReviewSoD) {
		t.Fatalf("original preparer resolution should violate separation of duty, got %v", err)
	}
	// Unknown outcome rejected.
	if _, err := env.reviews.Resolve(ctx, review.ID,
		dto.ResolvePriorityReview{ExpectedVersion: 1, Outcome: "discard", ReplacementBasis: "非法结果"},
		"reviewer", model.RoleReviewer, "req-outcome"); !errors.Is(err, ErrReviewOutcome) {
		t.Fatalf("invalid outcome should be rejected, got %v", err)
	}
	// Stale review version: conflict and original decision untouched.
	if _, err := env.reviews.Resolve(ctx, review.ID,
		dto.ResolvePriorityReview{ExpectedVersion: 99, Outcome: "upgrade", ReplacementBasis: "过期版本"},
		"reviewer", model.RoleReviewer, "req-stale"); !errors.Is(err, repository.ErrVersionConflict) {
		t.Fatalf("stale expected version should conflict, got %v", err)
	}
	decision, err = env.priorities.Get(ctx, review.DecisionID)
	if err != nil {
		t.Fatalf("reload decision after failures: %v", err)
	}
	if decision.Status != "restrict" || decision.Version != 2 || len(decision.Revisions) != 2 {
		t.Fatalf("failed resolutions must not overwrite original decision/versions, got status=%s version=%d revisions=%d",
			decision.Status, decision.Version, len(decision.Revisions))
	}
	pending, err := env.reviews.Get(ctx, review.ID)
	if err != nil {
		t.Fatalf("reload review after failures: %v", err)
	}
	if pending.Status != "pending" {
		t.Fatalf("failed resolution must leave review pending, got %s", pending.Status)
	}
}

func seedPendingReview(t *testing.T, env reviewTestEnv, decisionCode, defectCode, finalStatus string) model.PriorityReview {
	t.Helper()
	ctx := context.Background()
	facility := "K42 桥梁作业区"
	created, err := env.priorities.Create(ctx, priorityCreateInputAt(decisionCode, facility, "basis-v1"), "operator", "seed-pd-create")
	if err != nil {
		t.Fatalf("create decision: %v", err)
	}
	if _, err := env.priorities.Transition(ctx, created.ID,
		dto.TransitionRequest{Status: finalStatus, ExpectedVersion: 1, Reason: "independent finalization"},
		"reviewer", model.RoleReviewer, "seed-pd-final"); err != nil {
		t.Fatalf("finalize decision: %v", err)
	}
	defect, err := env.defects.Create(ctx, criticalDefectInput(defectCode, facility), "operator", "seed-df-create")
	if err != nil {
		t.Fatalf("create defect: %v", err)
	}
	verified, err := env.defects.Transition(ctx, defect.ID,
		dto.TransitionRequest{Status: "verified", ExpectedVersion: 1, Reason: "severe defect confirmed"},
		"operator", "seed-df-verify")
	if err != nil {
		t.Fatalf("verify defect: %v", err)
	}
	if len(verified.TriggeredReviews) != 1 {
		t.Fatalf("expected one triggered review, got %d", len(verified.TriggeredReviews))
	}
	return verified.TriggeredReviews[0]
}

func priorityCreateInputAt(code, facility, evidence string) dto.CreatePriorityDecision {
	input := priorityCreateInput(code, evidence)
	input.Facility = facility
	return input
}

func criticalDefectInput(code, facility string) dto.CreateDefectFinding {
	return dto.CreateDefectFinding{
		Code: code, Name: "严重桥梁缺陷", Description: "priority review test", Facility: facility,
		Owner: "infrastructure team", Category: "structural", RiskLevel: "critical", MetricValue: 95,
		MetricUnit: "score", EffectiveAt: time.Now().UTC(), Evidence: "严重裂缝照片与量测记录", RelatedCode: "IR-RV",
	}
}

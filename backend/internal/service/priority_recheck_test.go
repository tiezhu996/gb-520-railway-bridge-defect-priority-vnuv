package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/config"
	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/dto"
	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/model"
	"github.com/blueship581/railway-bridge-defect-priority/backend/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type recheckTestRig struct {
	decisions PriorityDecisionService
	defects   DefectFindingService
	rechecks  PriorityRecheckService
}

func newRecheckTestRig(t *testing.T) recheckTestRig {
	t.Helper()
	dsn := fmt.Sprintf("file:recheck-%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.DefectFinding{}, &model.PriorityDecision{}, &model.PriorityDecisionRevision{},
		&model.PriorityRecheck{}, &model.AuditLog{},
	); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	security := NewSecurityService(repository.NewSecurityRepository(db), config.Config{})
	decisionRepository := repository.NewPriorityDecisionRepository(db)
	recheckRepository := repository.NewPriorityRecheckRepository(db)
	rechecks := NewPriorityRecheckService(recheckRepository, decisionRepository, security)
	return recheckTestRig{
		decisions: NewPriorityDecisionService(decisionRepository, security),
		defects:   NewDefectFindingService(repository.NewDefectFindingRepository(db), security, rechecks),
		rechecks:  rechecks,
	}
}

// finalizeDecision creates a draft as operator and has an independent reviewer
// finalize to reach the requested terminal level, mirroring the production flow.
func finalizeDecision(t *testing.T, rig recheckTestRig, code, facility, level string) model.PriorityDecision {
	t.Helper()
	input := priorityCreateInput(code, "terminal evidence")
	input.Facility = facility
	created, err := rig.decisions.Create(context.Background(), input, "operator", "req-"+code+"-create")
	if err != nil {
		t.Fatalf("create decision: %v", err)
	}
	transition := dto.TransitionRequest{Status: level, ExpectedVersion: created.Version, Reason: "independent review to terminal"}
	final, err := rig.decisions.Transition(context.Background(), created.ID, transition, "reviewer", model.RoleReviewer, "req-"+code+"-final")
	if err != nil {
		t.Fatalf("finalize decision: %v", err)
	}
	return final
}

func verifyCriticalDefect(t *testing.T, rig recheckTestRig, code, facility string) model.DefectFinding {
	t.Helper()
	ctx := context.Background()
	created, err := rig.defects.Create(ctx, dto.CreateDefectFinding{
		Code: code, Name: "严重桥梁缺陷", Facility: facility, Owner: "infrastructure team",
		Category: "structural", RiskLevel: "critical", MetricValue: 95, MetricUnit: "score",
		EffectiveAt: time.Now().UTC(), Evidence: "裂缝影像与量测记录", RelatedCode: "IR-001",
	}, "operator", "req-"+code+"-create")
	if err != nil {
		t.Fatalf("create defect: %v", err)
	}
	verified, err := rig.defects.Transition(ctx, created.ID, dto.TransitionRequest{
		Status: "verified", ExpectedVersion: created.Version, Reason: "现场核实严重缺陷",
	}, "operator", "req-"+code+"-verify")
	if err != nil {
		t.Fatalf("verify defect: %v", err)
	}
	return verified
}

func singleRecheck(t *testing.T, rig recheckTestRig, defectID uint) model.PriorityRecheck {
	t.Helper()
	page, err := rig.rechecks.List(context.Background(), dto.RecheckQuery{DefectID: defectID, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list rechecks: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("expected exactly one recheck for defect %d, got total=%d", defectID, page.Total)
	}
	return page.Items[0]
}

func TestRecheckTriggeredBySevereDefectVerification(t *testing.T) {
	rig := newRecheckTestRig(t)
	ctx := context.Background()
	decision := finalizeDecision(t, rig, "PD-RC-1", "K42 桥梁作业区", "observe")

	defect := verifyCriticalDefect(t, rig, "DF-RC-1", "K42 桥梁作业区")
	recheck := singleRecheck(t, rig, defect.ID)
	if recheck.Status != model.RecheckStatusPending || recheck.PriorityDecisionID != decision.ID ||
		recheck.DecisionCode != decision.Code || recheck.TriggerLevel != "observe" || recheck.DecisionPreparedBy != "operator" {
		t.Fatalf("unexpected recheck payload: %+v", recheck)
	}

	// 原决定在复查待办期间继续生效且版本不变。
	unchanged, err := rig.decisions.Get(ctx, decision.ID)
	if err != nil {
		t.Fatalf("get decision: %v", err)
	}
	if unchanged.Status != "observe" || unchanged.Version != decision.Version || len(unchanged.Revisions) != 2 {
		t.Fatalf("original decision must stay effective while recheck is pending: %+v", unchanged)
	}

	// 同一缺陷并发/重复触发只生成一条。
	if err := rig.rechecks.TriggerForVerifiedDefect(ctx, defect, "operator", "req-dup"); err != nil {
		t.Fatalf("duplicate trigger must not fail: %v", err)
	}
	singleRecheck(t, rig, defect.ID)
}

func TestRecheckNotTriggeredForOrdinaryDefectOrWithoutTerminalDecision(t *testing.T) {
	rig := newRecheckTestRig(t)
	ctx := context.Background()
	finalizeDecision(t, rig, "PD-RC-2", "K42 桥梁作业区", "restrict")

	// 普通缺陷（非严重）核实不触发。
	ordinary, err := rig.defects.Create(ctx, dto.CreateDefectFinding{
		Code: "DF-RC-2", Name: "一般桥梁缺陷", Facility: "K42 桥梁作业区", Owner: "infrastructure team",
		Category: "structural", RiskLevel: "high", MetricValue: 40, MetricUnit: "score",
		EffectiveAt: time.Now().UTC(), Evidence: "常规量测记录", RelatedCode: "IR-001",
	}, "operator", "req-ordinary-create")
	if err != nil {
		t.Fatalf("create ordinary defect: %v", err)
	}
	if _, err := rig.defects.Transition(ctx, ordinary.ID, dto.TransitionRequest{
		Status: "verified", ExpectedVersion: ordinary.Version, Reason: "现场核实",
	}, "operator", "req-ordinary-verify"); err != nil {
		t.Fatalf("verify ordinary defect: %v", err)
	}
	page, err := rig.rechecks.List(ctx, dto.RecheckQuery{DefectID: ordinary.ID, Page: 1, PageSize: 10})
	if err != nil || page.Total != 0 {
		t.Fatalf("ordinary defect must not trigger recheck, total=%d err=%v", page.Total, err)
	}

	// 同桥没有观察/限速终态决定时不触发。
	isolated := verifyCriticalDefect(t, rig, "DF-RC-3", "K99 无决定桥梁")
	page, err = rig.rechecks.List(ctx, dto.RecheckQuery{DefectID: isolated.ID, Page: 1, PageSize: 10})
	if err != nil || page.Total != 0 {
		t.Fatalf("no terminal decision on bridge must not trigger recheck, total=%d err=%v", page.Total, err)
	}
}

func TestRecheckResolveEscalateKeepsVersionChain(t *testing.T) {
	rig := newRecheckTestRig(t)
	ctx := context.Background()
	decision := finalizeDecision(t, rig, "PD-RC-4", "K42 桥梁作业区", "restrict")
	defect := verifyCriticalDefect(t, rig, "DF-RC-4", "K42 桥梁作业区")
	recheck := singleRecheck(t, rig, defect.ID)

	// 处理人必须是非原拟制人的复查员或管理员。
	if _, err := rig.rechecks.Resolve(ctx, recheck.ID, dto.ResolvePriorityRecheck{Action: "escalate", Basis: "复测确认病害发展"}, "operator", model.RoleReviewer, "req-self"); !errors.Is(err, ErrRecheckSeparation) {
		t.Fatalf("original preparer must not resolve, got %v", err)
	}
	if _, err := rig.rechecks.Resolve(ctx, recheck.ID, dto.ResolvePriorityRecheck{Action: "escalate", Basis: "复测确认病害发展"}, "someone", model.RoleOperator, "req-role"); !errors.Is(err, ErrReviewRole) {
		t.Fatalf("operator role must not resolve, got %v", err)
	}

	resolved, err := rig.rechecks.Resolve(ctx, recheck.ID, dto.ResolvePriorityRecheck{Action: "escalate", Basis: "复测确认病害发展，需立即处置"}, "reviewer", model.RoleReviewer, "req-escalate")
	if err != nil {
		t.Fatalf("escalate recheck: %v", err)
	}
	if resolved.Status != model.RecheckStatusEscalated || resolved.HandledBy != "reviewer" || resolved.Basis == "" || resolved.HandledAt == nil {
		t.Fatalf("unexpected resolved recheck: %+v", resolved)
	}

	escalated, err := rig.decisions.Get(ctx, decision.ID)
	if err != nil {
		t.Fatalf("get escalated decision: %v", err)
	}
	if escalated.Status != "urgent" || escalated.Version != decision.Version+1 || len(escalated.Revisions) != 3 {
		t.Fatalf("escalation must append one urgent version, got %+v", escalated)
	}
	latest := escalated.Revisions[len(escalated.Revisions)-1]
	if latest.Actor != "reviewer" || latest.RequestID != "req-escalate" || latest.Reason == "" || latest.Snapshot == "" {
		t.Fatalf("escalation revision lost evidence: %+v", latest)
	}

	// 已处理的复查不可再次处理，失败不得覆盖原决定和版本。
	if _, err := rig.rechecks.Resolve(ctx, recheck.ID, dto.ResolvePriorityRecheck{Action: "maintain", Basis: "重复处理"}, "admin", model.RoleAdmin, "req-replay"); !errors.Is(err, ErrRecheckClosed) {
		t.Fatalf("resolved recheck must reject further actions, got %v", err)
	}
	after, err := rig.decisions.Get(ctx, decision.ID)
	if err != nil {
		t.Fatalf("get decision after replay: %v", err)
	}
	if after.Status != "urgent" || after.Version != escalated.Version || len(after.Revisions) != 3 {
		t.Fatalf("failed replay must not overwrite decision or versions: %+v", after)
	}
}

func TestRecheckResolveMaintainAndReleaseKeepDecision(t *testing.T) {
	rig := newRecheckTestRig(t)
	ctx := context.Background()

	maintainedDecision := finalizeDecision(t, rig, "PD-RC-5", "K42 桥梁作业区", "observe")
	maintainDefect := verifyCriticalDefect(t, rig, "DF-RC-5", "K42 桥梁作业区")
	maintainRecheck := singleRecheck(t, rig, maintainDefect.ID)
	resolved, err := rig.rechecks.Resolve(ctx, maintainRecheck.ID, dto.ResolvePriorityRecheck{Action: "maintain", Basis: "既有观察周期仍覆盖风险"}, "admin", model.RoleAdmin, "req-maintain")
	if err != nil {
		t.Fatalf("maintain recheck: %v", err)
	}
	if resolved.Status != model.RecheckStatusMaintained || resolved.HandledBy != "admin" {
		t.Fatalf("unexpected maintained recheck: %+v", resolved)
	}
	kept, err := rig.decisions.Get(ctx, maintainedDecision.ID)
	if err != nil {
		t.Fatalf("get maintained decision: %v", err)
	}
	if kept.Status != "observe" || kept.Version != maintainedDecision.Version || len(kept.Revisions) != 2 {
		t.Fatalf("maintain must keep the original decision and version: %+v", kept)
	}

	releasedDecision := finalizeDecision(t, rig, "PD-RC-6", "K7 桥梁作业区", "restrict")
	releaseDefect := verifyCriticalDefect(t, rig, "DF-RC-6", "K7 桥梁作业区")
	releaseRecheck := singleRecheck(t, rig, releaseDefect.ID)
	resolved, err = rig.rechecks.Resolve(ctx, releaseRecheck.ID, dto.ResolvePriorityRecheck{Action: "release", Basis: "缺陷复测降级，解除本次复查"}, "reviewer", model.RoleReviewer, "req-release")
	if err != nil {
		t.Fatalf("release recheck: %v", err)
	}
	if resolved.Status != model.RecheckStatusReleased {
		t.Fatalf("unexpected released recheck: %+v", resolved)
	}
	kept, err = rig.decisions.Get(ctx, releasedDecision.ID)
	if err != nil {
		t.Fatalf("get released decision: %v", err)
	}
	if kept.Status != "restrict" || kept.Version != releasedDecision.Version || len(kept.Revisions) != 2 {
		t.Fatalf("release must keep the original decision and version: %+v", kept)
	}
}

func TestRecheckEscalateFailureLeavesDecisionUntouched(t *testing.T) {
	rig := newRecheckTestRig(t)
	ctx := context.Background()
	decision := finalizeDecision(t, rig, "PD-RC-7", "K42 桥梁作业区", "observe")

	// 同桥两条严重缺陷各自触发一条复查，指向同一个终态决定。
	firstDefect := verifyCriticalDefect(t, rig, "DF-RC-7", "K42 桥梁作业区")
	secondDefect := verifyCriticalDefect(t, rig, "DF-RC-8", "K42 桥梁作业区")
	first := singleRecheck(t, rig, firstDefect.ID)
	second := singleRecheck(t, rig, secondDefect.ID)
	if first.PriorityDecisionID != decision.ID || second.PriorityDecisionID != decision.ID {
		t.Fatalf("both rechecks should target the bridge decision: %+v %+v", first, second)
	}

	if _, err := rig.rechecks.Resolve(ctx, first.ID, dto.ResolvePriorityRecheck{Action: "escalate", Basis: "首次升级"}, "reviewer", model.RoleReviewer, "req-first"); err != nil {
		t.Fatalf("first escalate: %v", err)
	}
	// 决定已被升级为 urgent，第二条复查再次升级必须失败。
	if _, err := rig.rechecks.Resolve(ctx, second.ID, dto.ResolvePriorityRecheck{Action: "escalate", Basis: "重复升级"}, "admin", model.RoleAdmin, "req-second"); !errors.Is(err, ErrDecisionLocked) {
		t.Fatalf("escalating a non-terminal decision must fail, got %v", err)
	}

	// 失败不得覆盖原决定和版本，第二条复查保持待处理。
	frozen, err := rig.decisions.Get(ctx, decision.ID)
	if err != nil {
		t.Fatalf("get decision: %v", err)
	}
	if frozen.Status != "urgent" || frozen.Version != decision.Version+1 || len(frozen.Revisions) != 3 {
		t.Fatalf("failed escalation must not overwrite original decision and version: %+v", frozen)
	}
	pending, err := rig.rechecks.Get(ctx, second.ID)
	if err != nil {
		t.Fatalf("get second recheck: %v", err)
	}
	if pending.Status != model.RecheckStatusPending {
		t.Fatalf("failed escalation must keep recheck pending: %+v", pending)
	}

	// 待处理复查仍可维持原决定。
	resolved, err := rig.rechecks.Resolve(ctx, second.ID, dto.ResolvePriorityRecheck{Action: "maintain", Basis: "升级已覆盖风险，维持其余条款"}, "admin", model.RoleAdmin, "req-maintain")
	if err != nil {
		t.Fatalf("maintain after failed escalate: %v", err)
	}
	if resolved.Status != model.RecheckStatusMaintained {
		t.Fatalf("unexpected recheck after failed escalate: %+v", resolved)
	}
}

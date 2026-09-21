package constants

// Shared status values are mirrored in frontend/src/types/status.ts. Keeping
// the lists explicit makes state-machine drift visible during code review.

type DefectState string

const (
	DefectStateNew        DefectState = "new"
	DefectStateVerified   DefectState = "verified"
	DefectStateMonitoring DefectState = "monitoring"
	DefectStateMitigated  DefectState = "mitigated"
	DefectStateClosed     DefectState = "closed"
)

var AllDefectState = []string{"new", "verified", "monitoring", "mitigated", "closed"}

type PriorityLevel string

const (
	PriorityLevelObserve  PriorityLevel = "observe"
	PriorityLevelRestrict PriorityLevel = "restrict"
	PriorityLevelUrgent   PriorityLevel = "urgent"
)

var AllPriorityLevel = []string{"observe", "restrict", "urgent"}

// PriorityReviewStatus covers the lifecycle of the mandatory re-review that is
// triggered when a severe defect is verified on a bridge already under an
// observe/restrict decision.
type PriorityReviewStatus string

const (
	PriorityReviewStatusPending    PriorityReviewStatus = "pending"
	PriorityReviewStatusMaintained PriorityReviewStatus = "maintained"
	PriorityReviewStatusUpgraded   PriorityReviewStatus = "upgraded"
	PriorityReviewStatusReleased   PriorityReviewStatus = "released"
)

var AllPriorityReviewStatus = []string{"pending", "maintained", "upgraded", "released"}

// PriorityReviewOutcome are the only resolutions a reviewer may submit.
const (
	PriorityReviewOutcomeMaintain = "maintain"
	PriorityReviewOutcomeUpgrade  = "upgrade"
	PriorityReviewOutcomeRelease  = "release"
)

var AllPriorityReviewOutcome = []string{
	PriorityReviewOutcomeMaintain, PriorityReviewOutcomeUpgrade, PriorityReviewOutcomeRelease,
}

// PriorityDecisionReleased is the terminal state written when a re-review
// releases an existing observe/restrict decision. It is reachable only through
// the re-review resolution flow, never through the normal transition graph.
const PriorityDecisionReleased = "released"

// SevereRiskLevels are the defect risk levels that force a priority re-review
// once the defect is verified.
var SevereRiskLevels = map[string]bool{"high": true, "critical": true}

// ReviewTriggerStatuses are the terminal priority decisions that a new severe
// finding on the same bridge forces back into re-review.
var ReviewTriggerStatuses = map[string]bool{
	string(PriorityLevelObserve):  true,
	string(PriorityLevelRestrict): true,
}

var BridgeAssetTransitions = map[string]map[string]bool{
	"active":     {"restricted": true, "closed": true},
	"restricted": {"closed": true, "retired": true, "active": true},
	"closed":     {"retired": true, "restricted": true},
	"retired":    {"closed": true},
}

var InspectionRoundTransitions = map[string]map[string]bool{
	"planned":   {"running": true, "review": true},
	"running":   {"review": true, "completed": true, "planned": true},
	"review":    {"completed": true, "running": true},
	"completed": {"review": true},
}

var DefectFindingTransitions = map[string]map[string]bool{
	"new":        {"verified": true, "monitoring": true},
	"verified":   {"monitoring": true, "mitigated": true, "new": true},
	"monitoring": {"mitigated": true, "closed": true, "verified": true},
	"mitigated":  {"closed": true, "monitoring": true},
	"closed":     {"mitigated": true},
}

var PriorityDecisionTransitions = map[string]map[string]bool{
	"draft":    {"observe": true, "restrict": true, "urgent": true},
	"observe":  {},
	"restrict": {},
	"urgent":   {},
}

func CanTransition(graph map[string]map[string]bool, from, to string) bool {
	targets, exists := graph[from]
	return exists && targets[to]
}

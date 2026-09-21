package dto

// RecheckQuery filters 优先级复查列表. DefectID and DecisionID let the defect
// and priority workbenches fetch only the rechecks relevant to their rows.
type RecheckQuery struct {
	Page       int    `form:"page"`
	PageSize   int    `form:"pageSize"`
	Search     string `form:"search"`
	Status     string `form:"status"`
	DefectID   uint   `form:"defectId"`
	DecisionID uint   `form:"decisionId"`
}

// ResolvePriorityRecheck is the write contract for handling a pending recheck.
// Action is one of maintain/escalate/release; Basis retains the 替代依据.
type ResolvePriorityRecheck struct {
	Action string `json:"action" binding:"required,oneof=maintain escalate release"`
	Basis  string `json:"basis" binding:"required,min=3,max=500"`
}

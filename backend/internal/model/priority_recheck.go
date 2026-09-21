package model

import "time"

// Recheck lifecycle statuses. pending is the only state that accepts a
// resolution; the rest are terminal outcomes.
const (
	RecheckStatusPending    = "pending"
	RecheckStatusMaintained = "maintained"
	RecheckStatusEscalated  = "escalated"
	RecheckStatusReleased   = "released"
)

// Recheck resolution actions accepted by the resolve endpoint.
const (
	RecheckActionMaintain = "maintain"
	RecheckActionEscalate = "escalate"
	RecheckActionRelease  = "release"
)

// RecheckActionStatus maps a resolve action to the terminal recheck status.
var RecheckActionStatus = map[string]string{
	RecheckActionMaintain: RecheckStatusMaintained,
	RecheckActionEscalate: RecheckStatusEscalated,
	RecheckActionRelease:  RecheckStatusReleased,
}

// PriorityRecheck models 严重缺陷触发的优先级复查. It is created automatically
// when a severe defect is verified while the same bridge already carries a
// terminal observe/restrict decision. The original decision keeps taking
// effect until an independent reviewer resolves the recheck. DefectFindingID
// is unique so concurrent triggers for the same defect yield exactly one row.
type PriorityRecheck struct {
	ID                 uint       `json:"id" gorm:"primaryKey"`
	DefectFindingID    uint       `json:"defectFindingId" gorm:"uniqueIndex;not null"`
	DefectCode         string     `json:"defectCode" gorm:"size:64;index;not null"`
	PriorityDecisionID uint       `json:"priorityDecisionId" gorm:"index;not null"`
	DecisionCode       string     `json:"decisionCode" gorm:"size:64;index;not null"`
	DecisionPreparedBy string     `json:"decisionPreparedBy" gorm:"size:80;not null"`
	TriggerLevel       string     `json:"triggerLevel" gorm:"size:40;not null"`
	Status             string     `json:"status" gorm:"size:40;index;not null;default:pending"`
	Basis              string     `json:"basis" gorm:"size:500"`
	HandledBy          string     `json:"handledBy" gorm:"size:80;index"`
	HandledAt          *time.Time `json:"handledAt"`
	RequestID          string     `json:"requestId" gorm:"size:64;index"`
	CreatedAt          time.Time  `json:"createdAt" gorm:"index"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

func (item PriorityRecheck) TableName() string { return "priority_rechecks" }

package model

import "time"

// PriorityReview （严重缺陷触发优先级复查） is generated when a severe defect is
// verified while the same bridge already has an observe/restrict terminal
// priority decision. The original decision stays in force while the review is
// pending. The unique index on DefectID guarantees at most one review is
// generated for the same defect even under concurrent verification requests.
type PriorityReview struct {
	BaseModel
	// DefectID/DefectCode identify the severe defect whose verification
	// triggered the review. DefectID is unique so one defect generates one
	// review at most.
	DefectID   uint   `json:"defectId" gorm:"uniqueIndex;not null"`
	DefectCode string `json:"defectCode" gorm:"size:64;index;not null"`
	// Facility is the bridge identifier shared by the defect and the decision.
	Facility string `json:"facility" gorm:"size:120;index;not null"`
	// TriggeredBy is the user who verified the defect (system side records the
	// verification actor); TriggerRequestID ties generation to its request.
	TriggeredBy      string    `json:"triggeredBy" gorm:"size:80;index;not null"`
	TriggerRequestID string    `json:"triggerRequestId" gorm:"size:64;index;not null"`
	TriggeredAt      time.Time `json:"triggeredAt" gorm:"index;not null"`
	// DecisionID/DecisionCode/OriginalStatus snapshot the triggered terminal
	// decision at generation time. The decision itself is never mutated until
	// the review is resolved (and even then only via append-only revisions).
	DecisionID     uint   `json:"decisionId" gorm:"index;not null"`
	DecisionCode   string `json:"decisionCode" gorm:"size:64;index;not null"`
	OriginalStatus string `json:"originalStatus" gorm:"size:40;index;not null"`
	// OriginalPreparedBy enforces separation of duty: the reviewer must differ
	// from the preparer of the original decision.
	OriginalPreparedBy string `json:"originalPreparedBy" gorm:"size:80;not null"`
	// Resolution fields, filled once the review leaves pending.
	Outcome        string `json:"outcome" gorm:"size:20;index"`
	ReplacementBasis string  `json:"replacementBasis" gorm:"size:2000"`
	ReviewedBy     string `json:"reviewedBy" gorm:"size:80;index"`
	ReviewRequestID string `json:"reviewRequestId" gorm:"size:64;index"`
	ReviewedAt     *time.Time `json:"reviewedAt"`
	// ResultingDecisionVersion records the appended decision version for
	// upgrade/release resolutions; maintain keeps the original version.
	ResultingDecisionVersion uint `json:"resultingDecisionVersion"`
}

func (item *PriorityReview) GetBase() *BaseModel { return &item.BaseModel }

func (item PriorityReview) TableName() string { return "priority_reviews" }

var PriorityReviewInitialStatus = "pending"

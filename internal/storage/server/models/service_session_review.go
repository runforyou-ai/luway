//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ServiceSessionReview 表示客服处理周期最近一次关闭后的满意度与 AI 客服、真人客服质检结论。
type ServiceSessionReview struct {
	bun.BaseModel `bun:"table:service_session_reviews,alias:ssr"`

	ID                string    `bun:"id,pk"`
	CreatedAt         time.Time `bun:"created_at"`
	UpdatedAt         time.Time `bun:"updated_at"`
	OrganizationID    string    `bun:"organization_id"`
	ServiceSessionID  string    `bun:"service_session_id"`
	ClosedAt          time.Time `bun:"closed_at"`
	Satisfaction      *string   `bun:"satisfaction"`
	AIIncorrect       *bool     `bun:"ai_incorrect"`
	AIMissedHandoff   *bool     `bun:"ai_missed_handoff"`
	AIPoorAttitude    *bool     `bun:"ai_poor_attitude"`
	HumanIncorrect    *bool     `bun:"human_incorrect"`
	HumanPoorAttitude *bool     `bun:"human_poor_attitude"`
}

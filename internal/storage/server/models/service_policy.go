//go:build server

package models

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// ServicePolicy 表示工作区内按服务对象独立配置的服务策略。
type ServicePolicy struct {
	bun.BaseModel `bun:"table:service_policies,alias:sp"`

	WorkspaceID             string                         `bun:"workspace_id,pk"`
	Audience                domain.ServiceAudience         `bun:"audience,pk"`
	BusinessHoursEnabled    bool                           `bun:"business_hours_enabled"`
	BusinessHoursTimeZone   string                         `bun:"business_hours_time_zone"`
	BusinessHoursWeekly     [][]domain.BusinessHoursPeriod `bun:"business_hours_weekly,type:jsonb"`
	BusinessHoursOverrides  []domain.BusinessHoursOverride `bun:"business_hours_overrides,type:jsonb"`
	ResponseReminderMinutes int                            `bun:"response_reminder_minutes"`
	ResponseReclaimMinutes  int                            `bun:"response_reclaim_minutes"`
	QueueReminderMinutes    int                            `bun:"queue_reminder_minutes"`
	AIFollowUpMinutes       int                            `bun:"ai_follow_up_minutes"`
	AICloseMinutes          int                            `bun:"ai_close_minutes"`
	DecisionModelID         *string                        `bun:"decision_model_id"`
	SummaryModelID          *string                        `bun:"summary_model_id"`
	SummaryLocale           string                         `bun:"summary_locale"`
	CreatedAt               time.Time                      `bun:"created_at"`
	UpdatedAt               time.Time                      `bun:"updated_at"`
}

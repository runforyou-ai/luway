//go:build server

package models

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// CustomerServiceSetting 表示工作区客服设置，每个工作区一行，创建工作区时写入。
type CustomerServiceSetting struct {
	bun.BaseModel `bun:"table:customer_service_settings,alias:css"`

	WorkspaceID             string                         `bun:"workspace_id,pk"`
	BusinessHoursEnabled    bool                           `bun:"business_hours_enabled"`
	BusinessHoursTimeZone   string                         `bun:"business_hours_time_zone"`
	BusinessHoursWeekly     [][]domain.BusinessHoursPeriod `bun:"business_hours_weekly,type:jsonb"`
	BusinessHoursOverrides  []domain.BusinessHoursOverride `bun:"business_hours_overrides,type:jsonb"`
	ResponseReminderMinutes int                            `bun:"response_reminder_minutes"`
	ResponseReclaimMinutes  int                            `bun:"response_reclaim_minutes"`
	QueueReminderMinutes    int                            `bun:"queue_reminder_minutes"`
	AIFollowUpMinutes       int                            `bun:"ai_follow_up_minutes"`
	AICloseMinutes          int                            `bun:"ai_close_minutes"`
	CustomerIdentitySecret  *string                        `bun:"customer_identity_secret"`
	DecisionModelID         *string                        `bun:"decision_model_id"`
	SummaryModelID          *string                        `bun:"summary_model_id"`
	TranslationModelID      *string                        `bun:"translation_model_id"`
	SummaryLocale           string                         `bun:"summary_locale"`
	CreatedAt               time.Time                      `bun:"created_at"`
	UpdatedAt               time.Time                      `bun:"updated_at"`
}

//go:build server

package models

import (
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/uptrace/bun"
)

// CustomerServiceSetting 表示工作区客服设置，每个工作区一行，创建工作区时写入。
type CustomerServiceSetting struct {
	bun.BaseModel `bun:"table:customer_service_settings,alias:css"`

	OrganizationID             string                         `bun:"organization_id,pk"`
	BusinessHoursEnabled       bool                           `bun:"business_hours_enabled"`
	BusinessHoursTimeZone      string                         `bun:"business_hours_time_zone"`
	BusinessHoursWeekly        [][]domain.BusinessHoursPeriod `bun:"business_hours_weekly,type:jsonb"`
	BusinessHoursOverrides     []domain.BusinessHoursOverride `bun:"business_hours_overrides,type:jsonb"`
	ResponseReminderMinutes    int                            `bun:"response_reminder_minutes"`
	ResponseReclaimMinutes     int                            `bun:"response_reclaim_minutes"`
	QueueReminderMinutes       int                            `bun:"queue_reminder_minutes"`
	AIFollowUpMinutes          int                            `bun:"ai_follow_up_minutes"`
	AICloseMinutes             int                            `bun:"ai_close_minutes"`
	CustomerIdentitySecret     *string                        `bun:"customer_identity_secret"`
	DecisionProviderID         *string                        `bun:"decision_provider_id"`
	DecisionModelIdentifier    *string                        `bun:"decision_model_identifier"`
	SummaryProviderID          *string                        `bun:"summary_provider_id"`
	SummaryModelIdentifier     *string                        `bun:"summary_model_identifier"`
	SummaryLocale              string                         `bun:"summary_locale"`
	TranslationProviderID      *string                        `bun:"translation_provider_id"`
	TranslationModelIdentifier *string                        `bun:"translation_model_identifier"`
	CreatedAt                  time.Time                      `bun:"created_at"`
	UpdatedAt                  time.Time                      `bun:"updated_at"`
}

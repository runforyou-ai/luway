package runstream

import "github.com/runforyou-ai/cervi/internal/domain"

// PlanTask 是运行任务清单中的一项任务。
type PlanTask struct {
	ID         string                     `json:"id"`
	Subject    string                     `json:"subject"`
	ActiveForm string                     `json:"activeForm,omitempty"`
	Status     domain.AgentPlanTaskStatus `json:"status"`
}

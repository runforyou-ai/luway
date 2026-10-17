//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// WorkspaceEvaluationSetting 保存工作区 AI 评测使用的判断模型。
type WorkspaceEvaluationSetting struct {
	bun.BaseModel   `bun:"table:workspace_evaluation_settings,alias:wes"`
	WorkspaceID     string    `bun:"workspace_id,pk"`
	DecisionModelID *string   `bun:"decision_model_id"`
	CreatedAt       time.Time `bun:"created_at"`
	UpdatedAt       time.Time `bun:"updated_at"`
}

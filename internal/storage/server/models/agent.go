//go:build server

package models

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// Agent 表示 PostgreSQL 中的 AI 员工，服务对象为 personal 时仅服务负责人本人。
type Agent struct {
	bun.BaseModel `bun:"table:agents,alias:a"`

	ID               string  `bun:"id,pk"`
	IdentityID       string  `bun:"identity_id"`
	WorkspaceID      string  `bun:"workspace_id"`
	ActiveRevisionID string  `bun:"active_revision_id"`
	Status           string  `bun:"status"`
	ComputerID       *string `bun:"computer_id"`
	// ComputerGrant 是服务型 AI 员工对所用工作区电脑的授权，个人 AI 员工与未使用电脑时为空。
	ComputerGrant *domain.ToolGrant `bun:"computer_grant,type:jsonb"`
	// LocalAgents 是 AI 员工启用的本机 Agent 名称。
	LocalAgents       []string                 `bun:"local_agents,type:jsonb"`
	ServiceAudiences  []domain.ServiceAudience `bun:"service_audiences,array"`
	HandoffTeamID     *string                  `bun:"handoff_team_id"`
	ResponsibleUserID *string                  `bun:"responsible_user_id"`
	PausedAt          *time.Time               `bun:"paused_at"`
	CreatedAt         time.Time                `bun:"created_at"`
	UpdatedAt         time.Time                `bun:"updated_at"`
}

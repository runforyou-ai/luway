//go:build server

package agent

import (
	"time"

	teamaction "github.com/runforyou-ai/cervi/internal/actions/team"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
)

// CreateInput 定义新增 AI 员工字段，AvatarFileID 为空时不设置头像。
type CreateInput struct {
	DisplayName      string
	TeamIDs          []string
	ServiceAudiences []domain.ServiceAudience
	AvatarFileID     string
	Execution        ExecutionInput
}

// UpdateInput 定义 AI 员工可编辑字段，AvatarFileID 为空时保留当前头像，HandoffTeamID 为空表示转人工进入公共队列，ResponsibleUserID 为空表示不指定负责人。
type UpdateInput struct {
	DisplayName       string
	TeamIDs           []string
	ServiceAudiences  []domain.ServiceAudience
	HandoffTeamID     string
	ResponsibleUserID string
	WorkStatus        domain.WorkStatus
	AvatarFileID      string
}

// Responsible 定义 AI 员工负责人及其账号状态。
type Responsible struct {
	UserID      string                `bun:"user_id"`
	DisplayName string                `bun:"display_name"`
	Email       string                `bun:"email"`
	Status      domain.IdentityStatus `bun:"status"`
}

// TeamSummary 定义 AI 员工所属团队摘要。
type TeamSummary = teamaction.Summary

// ListInput 定义 AI 员工目录查询条件。
type ListInput struct {
	Query    string
	Status   domain.IdentityStatus
	Page     int
	PageSize int
}

// Agent 定义 AI 员工信息，HandoffTeamID 为空表示转人工进入公共队列，Responsible 为空表示未指定负责人。
type Agent struct {
	ID               string                   `bun:"id"`
	IdentityID       string                   `bun:"identity_id"`
	DisplayName      string                   `bun:"display_name"`
	AvatarFileID     *string                  `bun:"avatar_file_id"`
	ServiceAudiences []domain.ServiceAudience `bun:"service_audiences,array"`
	HandoffTeamID    *string                  `bun:"handoff_team_id"`
	Responsible      *Responsible             `bun:"-"`
	Status           domain.IdentityStatus    `bun:"status"`
	WorkStatus       domain.WorkStatus        `bun:"work_status"`
	Teams            []TeamSummary
	Execution        Execution
	CreatedAt        time.Time `bun:"created_at"`
}

// ListItem 定义 AI 员工目录项。
type ListItem struct {
	ID           string                `bun:"id"`
	IdentityID   string                `bun:"identity_id"`
	DisplayName  string                `bun:"display_name"`
	AvatarFileID *string               `bun:"avatar_file_id"`
	Status       domain.IdentityStatus `bun:"status"`
	WorkStatus   domain.WorkStatus     `bun:"work_status"`
	Teams        []TeamSummary
	Execution    ExecutionSummary
	CreatedAt    time.Time `bun:"created_at"`
}

// ListOutput 定义 AI 员工分页结果。
type ListOutput struct {
	Agents []ListItem
	Page   common.PageInfo
}

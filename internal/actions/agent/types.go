//go:build server

package agent

import (
	"slices"
	"time"

	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
)

// CreateInput 定义新增 AI 员工字段，AvatarFileID 为空时不设置头像。
type CreateInput struct {
	DisplayName      string
	TeamIDs          []string
	ServiceAudiences []domain.ServiceAudience
	AvatarFileID     string
	Execution        ExecutionInput
}

// UpdateInput 定义 AI 员工可编辑字段，AvatarFileID 为空时保留当前头像，HandoffTeamID 为空表示转人工进入公共队列，ResponsibleUserID 为空表示不指定负责人，ComputerID 为空表示不使用工作区电脑。
type UpdateInput struct {
	DisplayName       string
	TeamIDs           []string
	ServiceAudiences  []domain.ServiceAudience
	HandoffTeamID     string
	ResponsibleUserID string
	ComputerID        string
	ComputerGrant     domain.ToolGrant // 对所用工作区电脑的授权，ComputerID 为空时忽略。
	LocalAgents       []string         // 启用的工作区电脑上的本机 Agent 名称，ComputerID 为空时忽略。
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

// Computer 定义 AI 员工使用的工作区电脑、授权与启用的本机 Agent。
type Computer struct {
	ID          string           `bun:"computer_id"`
	Name        string           `bun:"computer_name"`
	Grant       domain.ToolGrant `bun:"computer_grant,type:jsonb"`
	LocalAgents []string         `bun:"local_agents,type:jsonb"`
}

// Agent 定义 AI 员工信息，HandoffTeamID 为空表示转人工进入公共队列，Responsible 为空表示未指定负责人，Computer 为空表示不使用工作区电脑。
type Agent struct {
	ID               string                   `bun:"id"`
	IdentityID       string                   `bun:"identity_id"`
	DisplayName      string                   `bun:"display_name"`
	AvatarFileID     *string                  `bun:"avatar_file_id"`
	ServiceAudiences []domain.ServiceAudience `bun:"service_audiences,array"`
	HandoffTeamID    *string                  `bun:"handoff_team_id"`
	Responsible      *Responsible             `bun:"-"`
	Computer         *Computer                `bun:"-"`
	Status           domain.IdentityStatus    `bun:"status"`
	WorkStatus       domain.WorkStatus        `bun:"work_status"`
	Teams            []TeamSummary
	Execution        Execution
	CreatedAt        time.Time `bun:"created_at"`
}

// ListItem 定义 AI 员工目录项，电脑与暂停字段只对个人 AI 员工有值。
type ListItem struct {
	ID                string                   `bun:"id"`
	IdentityID        string                   `bun:"identity_id"`
	DisplayName       string                   `bun:"display_name"`
	AvatarFileID      *string                  `bun:"avatar_file_id"`
	ServiceAudiences  []domain.ServiceAudience `bun:"service_audiences,array"`
	Status            domain.IdentityStatus    `bun:"status"`
	WorkStatus        domain.WorkStatus        `bun:"work_status"`
	PausedAt          *time.Time               `bun:"paused_at"`
	ComputerID        *string                  `bun:"computer_id"`
	ComputerName      *string                  `bun:"computer_name"`
	ComputerRevokedAt *time.Time               `bun:"computer_revoked_at"`
	ComputerOnline    bool                     `bun:"computer_online"`
	Teams             []TeamSummary
	Execution         ExecutionSummary
	CreatedAt         time.Time `bun:"created_at"`
}

// Personal 判断目录项是否为个人 AI 员工。
func (i ListItem) Personal() bool {
	return slices.Contains(i.ServiceAudiences, domain.ServiceAudiencePersonal)
}

// Presence 按账号状态、暂停、绑定电脑的撤销状态与是否在线计算个人 AI 员工当前是否可以处理新请求。
func (i ListItem) Presence() domain.PersonalAgentPresence {
	return domain.ResolvePersonalAgentPresence(i.Status, i.PausedAt != nil, i.ComputerRevokedAt != nil, i.ComputerOnline)
}

// ListOutput 定义 AI 员工分页结果。
type ListOutput struct {
	Agents []ListItem
	Page   common.PageInfo
}

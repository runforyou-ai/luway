package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// AccountStatus 表示部署内登录账号的状态。
type AccountStatus string

const (
	AccountStatusActive   AccountStatus = AccountStatus(domain.AccountStatusActive)
	AccountStatusInactive AccountStatus = AccountStatus(domain.AccountStatusInactive)
)

// RegistrationPolicy 表示部署的账号注册策略。
type RegistrationPolicy string

const (
	RegistrationPolicyOpen           RegistrationPolicy = RegistrationPolicy(domain.RegistrationPolicyOpen)
	RegistrationPolicyInvitationOnly RegistrationPolicy = RegistrationPolicy(domain.RegistrationPolicyInvitationOnly)
)

// WorkspaceCreationPolicy 表示部署内可以创建工作区的账号范围。
type WorkspaceCreationPolicy string

const (
	WorkspaceCreationPolicyAnyAccount      WorkspaceCreationPolicy = WorkspaceCreationPolicy(domain.WorkspaceCreationPolicyAnyAccount)
	WorkspaceCreationPolicyDeploymentAdmin WorkspaceCreationPolicy = WorkspaceCreationPolicy(domain.WorkspaceCreationPolicyDeploymentAdmin)
)

// InstanceCapabilities 定义部署实例当前生效的能力；WorkspaceLimit 为 0 表示不限工作区数量。
type InstanceCapabilities struct {
	WorkspaceLimit int  `json:"workspaceLimit"`
	CustomBranding bool `json:"customBranding"`
}

// DeploymentOverview 定义部署实例标识、服务端版本、安装时间、规模和实例能力。
type DeploymentOverview struct {
	InstanceID     string               `json:"instanceId"`
	Version        string               `json:"version"`
	InstalledAt    time.Time            `json:"installedAt"`
	AccountCount   int                  `json:"accountCount"`
	WorkspaceCount int                  `json:"workspaceCount"`
	Capabilities   InstanceCapabilities `json:"capabilities"`
}

// DeploymentSettings 定义部署注册策略和工作区创建策略。
type DeploymentSettings struct {
	RegistrationPolicy      RegistrationPolicy      `json:"registrationPolicy"`
	WorkspaceCreationPolicy WorkspaceCreationPolicy `json:"workspaceCreationPolicy"`
}

// DeploymentAccountListInput 定义部署账号列表的筛选与分页条件，Status 缺省为有效账号。
type DeploymentAccountListInput struct {
	Query    string        `json:"query" query:"query"`
	Status   AccountStatus `json:"status" query:"status,default=active"`
	Page     int           `json:"page" query:"page,default=1"`
	PageSize int           `json:"pageSize" query:"pageSize,default=50"`
}

// DeploymentAccount 定义部署账号列表中的一个账号及其加入的工作区数量。
type DeploymentAccount struct {
	ID                string        `json:"id"`
	Email             string        `json:"email"`
	DisplayName       string        `json:"displayName"`
	Status            AccountStatus `json:"status"`
	IsDeploymentAdmin bool          `json:"isDeploymentAdmin"`
	WorkspaceCount    int           `json:"workspaceCount"`
	CreatedAt         time.Time     `json:"createdAt"`
}

// DeploymentAccountList 定义部署账号分页结果。
type DeploymentAccountList struct {
	Accounts []DeploymentAccount `json:"accounts"`
	Page     PageInfo            `json:"page"`
}

// DeploymentWorkspaceListInput 定义部署工作区列表的关键词与分页条件。
type DeploymentWorkspaceListInput struct {
	Query    string `json:"query" query:"query"`
	Page     int    `json:"page" query:"page,default=1"`
	PageSize int    `json:"pageSize" query:"pageSize,default=50"`
}

// DeploymentWorkspace 定义部署工作区列表中的一个工作区及其有效成员数量。
type DeploymentWorkspace struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	MemberCount int       `json:"memberCount"`
	CreatedAt   time.Time `json:"createdAt"`
}

// DeploymentWorkspaceList 定义部署工作区分页结果。
type DeploymentWorkspaceList struct {
	Workspaces []DeploymentWorkspace `json:"workspaces"`
	Page       PageInfo              `json:"page"`
}

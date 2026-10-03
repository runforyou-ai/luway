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

// DeploymentOverview 定义部署实例标识、服务端版本、安装时间、规模、活跃情况和实例能力；活跃与新增按 StatisticsTimeZone 划分日期，StatsRebuilding 表示正在按新统计时区重建。
type DeploymentOverview struct {
	InstanceID         string                    `json:"instanceId"`
	Version            string                    `json:"version"`
	InstalledAt        time.Time                 `json:"installedAt"`
	StatisticsTimeZone string                    `json:"statisticsTimeZone"`
	StatsRebuilding    bool                      `json:"statsRebuilding"`
	AccountCount       int                       `json:"accountCount"`
	WorkspaceCount     int                       `json:"workspaceCount"`
	MemberCount        int                       `json:"memberCount"`
	Last7Days          DeploymentActivityWindow  `json:"last7Days"`
	Last30Days         DeploymentActivityWindow  `json:"last30Days"`
	Trend              []DeploymentDailyActivity `json:"trend"`
	Capabilities       InstanceCapabilities      `json:"capabilities"`
}

// DeploymentActivityWindow 定义截至今天若干天内去重后的活跃账号数和活跃工作区数，以及新增账号数和新增工作区数。
type DeploymentActivityWindow struct {
	ActiveAccounts   int `json:"activeAccounts"`
	ActiveWorkspaces int `json:"activeWorkspaces"`
	NewAccounts      int `json:"newAccounts"`
	NewWorkspaces    int `json:"newWorkspaces"`
}

// DeploymentDailyActivity 定义一天内的活跃账号数、活跃工作区数、新增账号数和新增工作区数，Date 为 YYYY-MM-DD。
type DeploymentDailyActivity struct {
	Date             string `json:"date"`
	ActiveAccounts   int    `json:"activeAccounts"`
	ActiveWorkspaces int    `json:"activeWorkspaces"`
	NewAccounts      int    `json:"newAccounts"`
	NewWorkspaces    int    `json:"newWorkspaces"`
}

// DeploymentSettings 定义部署注册策略、工作区创建策略和运营数据统计时区。
type DeploymentSettings struct {
	RegistrationPolicy      RegistrationPolicy      `json:"registrationPolicy"`
	WorkspaceCreationPolicy WorkspaceCreationPolicy `json:"workspaceCreationPolicy"`
	StatisticsTimeZone      string                  `json:"statisticsTimeZone"`
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

// DeploymentWorkspaceSort 表示部署工作区列表的排序方式，均为降序。
type DeploymentWorkspaceSort string

const (
	DeploymentWorkspaceSortCreatedAt   DeploymentWorkspaceSort = "created_at"
	DeploymentWorkspaceSortLastActive  DeploymentWorkspaceSort = "last_active"
	DeploymentWorkspaceSortMemberCount DeploymentWorkspaceSort = "member_count"
	DeploymentWorkspaceSortStorage     DeploymentWorkspaceSort = "storage"
)

// DeploymentWorkspaceListInput 定义部署工作区列表的关键词、状态、排序与分页条件；Status 为空表示全部状态，Sort 缺省按创建时间。
type DeploymentWorkspaceListInput struct {
	Query    string                  `json:"query" query:"query"`
	Status   WorkspaceStatus         `json:"status" query:"status"`
	Sort     DeploymentWorkspaceSort `json:"sort" query:"sort,default=created_at"`
	Page     int                     `json:"page" query:"page,default=1"`
	PageSize int                     `json:"pageSize" query:"pageSize,default=50"`
}

// DeploymentWorkspace 定义部署工作区列表中的一个工作区、状态与当前规模；HasDeploymentAdmin 表示有有效部署管理员成员，此时不能暂停；LastActiveOn 为最近有活跃的日期 YYYY-MM-DD，从未活跃时为空。
type DeploymentWorkspace struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	Slug               string          `json:"slug"`
	Status             WorkspaceStatus `json:"status"`
	MemberCount        int             `json:"memberCount"`
	AIEmployeeCount    int             `json:"aiEmployeeCount"`
	ChannelCount       int             `json:"channelCount"`
	DeviceCount        int             `json:"deviceCount"`
	HasDeploymentAdmin bool            `json:"hasDeploymentAdmin"`
	StorageBytes       int64           `json:"storageBytes"`
	LastActiveOn       *string         `json:"lastActiveOn"`
	CreatedAt          time.Time       `json:"createdAt"`
}

// DeploymentWorkspaceList 定义部署工作区分页结果。
type DeploymentWorkspaceList struct {
	Workspaces []DeploymentWorkspace `json:"workspaces"`
	Page       PageInfo              `json:"page"`
}

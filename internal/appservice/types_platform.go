package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// AccountStatus 表示平台内登录账号的状态。
type AccountStatus string

const (
	AccountStatusActive   AccountStatus = AccountStatus(domain.AccountStatusActive)
	AccountStatusInactive AccountStatus = AccountStatus(domain.AccountStatusInactive)
)

// RegistrationPolicy 表示平台的账号注册策略。
type RegistrationPolicy string

const (
	RegistrationPolicyOpen           RegistrationPolicy = RegistrationPolicy(domain.RegistrationPolicyOpen)
	RegistrationPolicyInvitationOnly RegistrationPolicy = RegistrationPolicy(domain.RegistrationPolicyInvitationOnly)
)

// WorkspaceCreationPolicy 表示平台内可以创建工作区的账号范围。
type WorkspaceCreationPolicy string

const (
	WorkspaceCreationPolicyAnyAccount    WorkspaceCreationPolicy = WorkspaceCreationPolicy(domain.WorkspaceCreationPolicyAnyAccount)
	WorkspaceCreationPolicyPlatformAdmin WorkspaceCreationPolicy = WorkspaceCreationPolicy(domain.WorkspaceCreationPolicyPlatformAdmin)
)

// LicenseStatus 表示授权状态。
type LicenseStatus string

const (
	LicenseStatusNone    LicenseStatus = LicenseStatus(domain.LicenseStatusNone)
	LicenseStatusActive  LicenseStatus = LicenseStatus(domain.LicenseStatusActive)
	LicenseStatusExpired LicenseStatus = LicenseStatus(domain.LicenseStatusExpired)
)

// Capabilities 定义平台当前生效的能力；WorkspaceLimit 为 0 表示不限工作区数量。
type Capabilities struct {
	WorkspaceLimit int  `json:"workspaceLimit"`
	CustomBranding bool `json:"customBranding"`
}

// PlatformOverview 定义服务器标识、服务端版本、安装时间、规模、活跃情况和平台能力；活跃与新增按 StatisticsTimeZone 划分日期，StatsRebuilding 表示正在按新统计时区重建。
type PlatformOverview struct {
	ServerID           string                  `json:"serverId"`
	Version            string                  `json:"version"`
	InstalledAt        time.Time               `json:"installedAt"`
	StatisticsTimeZone string                  `json:"statisticsTimeZone"`
	StatsRebuilding    bool                    `json:"statsRebuilding"`
	AccountCount       int                     `json:"accountCount"`
	WorkspaceCount     int                     `json:"workspaceCount"`
	MemberCount        int                     `json:"memberCount"`
	Last7Days          PlatformActivityWindow  `json:"last7Days"`
	Last30Days         PlatformActivityWindow  `json:"last30Days"`
	Trend              []PlatformDailyActivity `json:"trend"`
	Capabilities       Capabilities            `json:"capabilities"`
}

// License 定义服务器标识、授权状态、授权编号、客户、签发与到期时间和授权码授予的能力；未激活时只有服务器标识、状态和免费能力。
type License struct {
	ServerID     string        `json:"serverId"`
	Status       LicenseStatus `json:"status"`
	LicenseID    string        `json:"licenseId"`
	Customer     string        `json:"customer"`
	IssuedAt     *time.Time    `json:"issuedAt"`
	ExpiresAt    *time.Time    `json:"expiresAt"`
	Capabilities Capabilities  `json:"capabilities"`
}

// ActivateLicenseInput 定义平台管理员粘贴的授权码。
type ActivateLicenseInput struct {
	LicenseCode string `json:"licenseCode"`
}

// ActivateLicenseOnlineInput 定义平台管理员输入的激活码。
type ActivateLicenseOnlineInput struct {
	ActivationCode string `json:"activationCode"`
}

// PlatformActivityWindow 定义截至今天若干天内去重后的活跃账号数和活跃工作区数，以及新增账号数和新增工作区数。
type PlatformActivityWindow struct {
	ActiveAccounts   int `json:"activeAccounts"`
	ActiveWorkspaces int `json:"activeWorkspaces"`
	NewAccounts      int `json:"newAccounts"`
	NewWorkspaces    int `json:"newWorkspaces"`
}

// PlatformDailyActivity 定义一天内的活跃账号数、活跃工作区数、新增账号数和新增工作区数，Date 为 YYYY-MM-DD。
type PlatformDailyActivity struct {
	Date             string `json:"date"`
	ActiveAccounts   int    `json:"activeAccounts"`
	ActiveWorkspaces int    `json:"activeWorkspaces"`
	NewAccounts      int    `json:"newAccounts"`
	NewWorkspaces    int    `json:"newWorkspaces"`
}

// PlatformSettings 定义平台注册策略、工作区创建策略、运营数据统计时区和运行指标上报开关。
type PlatformSettings struct {
	RegistrationPolicy      RegistrationPolicy      `json:"registrationPolicy"`
	WorkspaceCreationPolicy WorkspaceCreationPolicy `json:"workspaceCreationPolicy"`
	StatisticsTimeZone      string                  `json:"statisticsTimeZone"`
	TelemetryEnabled        bool                    `json:"telemetryEnabled"`
}

// PlatformPoliciesInput 定义平台注册策略和工作区创建策略的修改值。
type PlatformPoliciesInput struct {
	RegistrationPolicy      RegistrationPolicy      `json:"registrationPolicy"`
	WorkspaceCreationPolicy WorkspaceCreationPolicy `json:"workspaceCreationPolicy"`
}

// PlatformTelemetryInput 定义运行指标上报开关的修改值。
type PlatformTelemetryInput struct {
	TelemetryEnabled bool `json:"telemetryEnabled"`
}

// PlatformStatisticsTimeZoneInput 定义运营数据统计时区的修改值。
type PlatformStatisticsTimeZoneInput struct {
	StatisticsTimeZone string `json:"statisticsTimeZone"`
}

// PlatformAccountListInput 定义平台账号列表的筛选与分页条件，Status 缺省为有效账号。
type PlatformAccountListInput struct {
	Query    string        `json:"query" query:"query"`
	Status   AccountStatus `json:"status" query:"status,default=active"`
	Page     int           `json:"page" query:"page,default=1"`
	PageSize int           `json:"pageSize" query:"pageSize,default=50"`
}

// PlatformAccount 定义平台账号列表中的一个账号及其加入的工作区数量。
type PlatformAccount struct {
	ID              string        `json:"id"`
	Email           string        `json:"email"`
	DisplayName     string        `json:"displayName"`
	Status          AccountStatus `json:"status"`
	IsPlatformAdmin bool          `json:"isPlatformAdmin"`
	WorkspaceCount  int           `json:"workspaceCount"`
	CreatedAt       time.Time     `json:"createdAt"`
}

// PlatformAccountList 定义平台账号分页结果。
type PlatformAccountList struct {
	Accounts []PlatformAccount `json:"accounts"`
	Page     PageInfo          `json:"page"`
}

// PlatformWorkspaceSort 表示平台工作区列表的排序方式，均为降序。
type PlatformWorkspaceSort string

const (
	PlatformWorkspaceSortCreatedAt   PlatformWorkspaceSort = "created_at"
	PlatformWorkspaceSortLastActive  PlatformWorkspaceSort = "last_active"
	PlatformWorkspaceSortMemberCount PlatformWorkspaceSort = "member_count"
	PlatformWorkspaceSortStorage     PlatformWorkspaceSort = "storage"
)

// PlatformWorkspaceListInput 定义平台工作区列表的关键词、状态、排序与分页条件；Status 为空表示全部状态，Sort 缺省按创建时间。
type PlatformWorkspaceListInput struct {
	Query    string                `json:"query" query:"query"`
	Status   WorkspaceStatus       `json:"status" query:"status"`
	Sort     PlatformWorkspaceSort `json:"sort" query:"sort,default=created_at"`
	Page     int                   `json:"page" query:"page,default=1"`
	PageSize int                   `json:"pageSize" query:"pageSize,default=50"`
}

// PlatformWorkspace 定义平台工作区列表中的一个工作区、状态与当前规模；HasPlatformAdmin 表示有有效平台管理员成员，此时不能暂停；LastActiveOn 为最近有活跃的日期 YYYY-MM-DD，从未活跃时为空。
type PlatformWorkspace struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Slug             string          `json:"slug"`
	Status           WorkspaceStatus `json:"status"`
	MemberCount      int             `json:"memberCount"`
	AIEmployeeCount  int             `json:"aiEmployeeCount"`
	ChannelCount     int             `json:"channelCount"`
	DeviceCount      int             `json:"deviceCount"`
	HasPlatformAdmin bool            `json:"hasPlatformAdmin"`
	StorageBytes     int64           `json:"storageBytes"`
	LastActiveOn     *string         `json:"lastActiveOn"`
	CreatedAt        time.Time       `json:"createdAt"`
}

// PlatformWorkspaceList 定义平台工作区分页结果。
type PlatformWorkspaceList struct {
	Workspaces []PlatformWorkspace `json:"workspaces"`
	Page       PageInfo            `json:"page"`
}

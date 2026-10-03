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

// PlatformOverview 定义服务器标识、安装时间、规模、活跃情况和平台能力；活跃与新增按 StatisticsTimeZone 划分日期，StatsRebuilding 表示正在按新统计时区重建。
type PlatformOverview struct {
	ServerID           string                  `json:"serverId"`
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

// License 定义服务器标识、授权状态、授权编号、客户、签发与到期时间、授权码授予的能力，以及与 control 同步时查不到本服务器授权的起始时间；未激活时只有服务器标识、状态和免费能力。
type License struct {
	ServerID         string        `json:"serverId"`
	Status           LicenseStatus `json:"status"`
	LicenseID        string        `json:"licenseId"`
	Customer         string        `json:"customer"`
	IssuedAt         *time.Time    `json:"issuedAt"`
	ExpiresAt        *time.Time    `json:"expiresAt"`
	Capabilities     Capabilities  `json:"capabilities"`
	ControlMissingAt *time.Time    `json:"controlMissingAt"`
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

// PlatformUsageSort 表示业务使用工作区列表的排序方式，均为降序。
type PlatformUsageSort string

const (
	PlatformUsageSortServiceSessions PlatformUsageSort = "service_sessions"
	PlatformUsageSortConversations   PlatformUsageSort = "conversations"
	PlatformUsageSortFirstResponse   PlatformUsageSort = "first_response"
	PlatformUsageSortKnowledgeGaps   PlatformUsageSort = "knowledge_gaps"
)

// PlatformUsageInput 定义业务使用的统计范围：最近 Days 天内关闭的客服周期。
type PlatformUsageInput struct {
	Days int `json:"days" query:"days,default=30"`
}

// PlatformWorkspaceUsageListInput 定义业务使用工作区列表的统计范围、排序与分页，Sort 缺省按服务周期数。
type PlatformWorkspaceUsageListInput struct {
	Days     int               `json:"days" query:"days,default=30"`
	Sort     PlatformUsageSort `json:"sort" query:"sort,default=service_sessions"`
	Page     int               `json:"page" query:"page,default=1"`
	PageSize int               `json:"pageSize" query:"pageSize,default=50"`
}

// PlatformUsageMetrics 定义业务使用指标，口径与工作区的 AI 表现和团队表现报表一致：ServiceSessions 为已关闭周期数，Conversations 为其所属会话去重数；
// AIClosed 为其中 AI 员工接待过的周期数，AIResolved 与 HandedOff 为其中 AI 独立解决与发生过转人工的周期数；首响为按工作时间计的真人首响（秒），没有样本时为空；KnowledgeGaps 为全部待处理的待补知识条数。
type PlatformUsageMetrics struct {
	ServiceSessions     int  `json:"serviceSessions"`
	Conversations       int  `json:"conversations"`
	AIClosed            int  `json:"aiClosed"`
	AIResolved          int  `json:"aiResolved"`
	HandedOff           int  `json:"handedOff"`
	FirstResponseMedian *int `json:"firstResponseMedian"`
	FirstResponseP90    *int `json:"firstResponseP90"`
	KnowledgeGaps       int  `json:"knowledgeGaps"`
}

// PlatformWorkspaceUsage 定义一个工作区的业务使用指标。
type PlatformWorkspaceUsage struct {
	ID      string               `json:"id"`
	Name    string               `json:"name"`
	Slug    string               `json:"slug"`
	Status  WorkspaceStatus      `json:"status"`
	Metrics PlatformUsageMetrics `json:"metrics"`
}

// PlatformWorkspaceUsageList 定义一页工作区业务使用指标。
type PlatformWorkspaceUsageList struct {
	Workspaces []PlatformWorkspaceUsage `json:"workspaces"`
	Page       PageInfo                 `json:"page"`
}

// PlatformRuntimeStatus 定义服务端版本与后台任务各队列的运行概况。
type PlatformRuntimeStatus struct {
	Version string              `json:"version"`
	Queues  []PlatformTaskQueue `json:"queues"`
}

// PlatformTaskQueue 定义一个后台任务队列的运行概况：Waiting 为已到执行时间仍在排队的任务数，OldestWaitingSince 为其中最早的到期时间，没有排队任务时为空；
// Running 为执行中的任务数，Retrying 为执行失败后等待重试的任务数，Paused 为所属工作区暂停而挂起的任务数，Failed 为近 7 天失败且不再重试的任务数。
type PlatformTaskQueue struct {
	Queue              string     `json:"queue"`
	Waiting            int        `json:"waiting"`
	OldestWaitingSince *time.Time `json:"oldestWaitingSince"`
	Running            int        `json:"running"`
	Retrying           int        `json:"retrying"`
	Paused             int        `json:"paused"`
	Failed             int        `json:"failed"`
}

// PlatformFailedTaskListInput 定义失败任务列表的分页。
type PlatformFailedTaskListInput struct {
	Page     int `json:"page" query:"page,default=1"`
	PageSize int `json:"pageSize" query:"pageSize,default=50"`
}

// PlatformFailedTask 定义一次等待重试或近 7 天内失败的后台任务运行；WorkspaceName 为所属工作区名称，平台级任务为空，FailedAt 为最近一次执行失败的时间。
type PlatformFailedTask struct {
	ID            string    `json:"id"`
	Action        string    `json:"action"`
	Queue         string    `json:"queue"`
	WorkspaceName *string   `json:"workspaceName"`
	Retrying      bool      `json:"retrying"`
	Attempt       int       `json:"attempt"`
	MaxAttempts   int       `json:"maxAttempts"`
	Error         string    `json:"error"`
	FailedAt      time.Time `json:"failedAt"`
}

// PlatformFailedTaskList 定义失败任务分页结果。
type PlatformFailedTaskList struct {
	Tasks []PlatformFailedTask `json:"tasks"`
	Page  PageInfo             `json:"page"`
}

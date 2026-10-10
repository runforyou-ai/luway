package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// AccountStatus 表示平台内登录账号的状态。
type AccountStatus = domain.AccountStatus

// RegistrationPolicy 表示平台的账号注册策略。
type RegistrationPolicy = domain.RegistrationPolicy

// WorkspaceCreationPolicy 表示平台内可以创建工作区的账号范围。
type WorkspaceCreationPolicy = domain.WorkspaceCreationPolicy

// LicenseStatus 表示授权状态。
type LicenseStatus = domain.LicenseStatus

// Capabilities 定义平台当前生效的能力；WorkspaceLimit 为 0 表示不限工作区数量，PushRelay 表示是否向移动设备发送离线推送。
type Capabilities struct {
	WorkspaceLimit int  `json:"workspaceLimit"`
	CustomBranding bool `json:"customBranding"`
	PushRelay      bool `json:"pushRelay"`
}

// PlatformOverview 定义平台规模与活跃情况；活跃与新增按 TimeZone 划分日期，StatsRebuilding 表示正在按新时区重建。
type PlatformOverview struct {
	TimeZone        string                  `json:"timeZone"`
	StatsRebuilding bool                    `json:"statsRebuilding"`
	AccountCount    int                     `json:"accountCount"`
	WorkspaceCount  int                     `json:"workspaceCount"`
	MemberCount     int                     `json:"memberCount"`
	Last7Days       PlatformActivityWindow  `json:"last7Days"`
	Last30Days      PlatformActivityWindow  `json:"last30Days"`
	Trend           []PlatformDailyActivity `json:"trend"`
}

// License 定义服务器标识、授权状态、授权编号、客户、签发与到期时间、当前生效的能力、与 control 同步时查不到本服务器授权的起始时间，以及与 control 的同步结果；未激活或已到期时能力为免费取值。
type License struct {
	ServerID         string                `json:"serverId"`
	Status           LicenseStatus         `json:"status"`
	LicenseID        string                `json:"licenseId"`
	Customer         string                `json:"customer"`
	IssuedAt         *time.Time            `json:"issuedAt"`
	ExpiresAt        *time.Time            `json:"expiresAt"`
	Capabilities     Capabilities          `json:"capabilities"`
	ControlMissingAt *time.Time            `json:"controlMissingAt"`
	Sync             PlatformControlStatus `json:"sync"`
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

// PlatformSettings 定义平台注册策略与工作区创建策略。
type PlatformSettings struct {
	RegistrationPolicy      RegistrationPolicy      `json:"registrationPolicy"`
	WorkspaceCreationPolicy WorkspaceCreationPolicy `json:"workspaceCreationPolicy"`
}

// PlatformPoliciesInput 定义平台注册策略和工作区创建策略的修改值。
type PlatformPoliciesInput struct {
	RegistrationPolicy      RegistrationPolicy      `json:"registrationPolicy" validate:"oneof=open invitation_only" msg:"field.registration_policy_invalid"`
	WorkspaceCreationPolicy WorkspaceCreationPolicy `json:"workspaceCreationPolicy" validate:"oneof=any_account platform_admin" msg:"field.workspace_creation_policy_invalid"`
}

// PlatformAccountListInput 定义平台账号列表的筛选与分页条件，Status 缺省为有效账号。
type PlatformAccountListInput struct {
	Query    string        `json:"query" query:"query"`
	Status   AccountStatus `json:"status" query:"status,default=active" validate:"oneof=active inactive" msg:"field.user_status_invalid"`
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
	Status   WorkspaceStatus       `json:"status" query:"status" validate:"omitempty,oneof=active suspended" msg:"field.platform_query_invalid"`
	Sort     PlatformWorkspaceSort `json:"sort" query:"sort,default=created_at" validate:"omitempty,oneof=created_at last_active member_count storage" msg:"field.platform_query_invalid"`
	Page     int                   `json:"page" query:"page,default=1"`
	PageSize int                   `json:"pageSize" query:"pageSize,default=50"`
}

// PlatformWorkspace 定义平台工作区列表中的一个工作区、状态与当前规模；HasPlatformAdmin 表示有有效平台管理员成员，此时不能暂停；LastActiveDays 为最近有活跃的日期距平台时区今天的天数，从未活跃时为空。
type PlatformWorkspace struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Slug             string          `json:"slug"`
	Status           WorkspaceStatus `json:"status"`
	MemberCount      int             `json:"memberCount"`
	AIEmployeeCount  int             `json:"aiEmployeeCount"`
	ChannelCount     int             `json:"channelCount"`
	ComputerCount    int             `json:"computerCount"`
	HasPlatformAdmin bool            `json:"hasPlatformAdmin"`
	StorageBytes     int64           `json:"storageBytes"`
	LastActiveDays   *int            `json:"lastActiveDays"`
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
	PlatformUsageSortModelTokens     PlatformUsageSort = "model_tokens"
)

// PlatformUsageInput 定义业务使用的统计范围：最近 Days 天内关闭的客服周期与开始的平台模型调用。
type PlatformUsageInput struct {
	Days int `json:"days" query:"days,default=30" validate:"min=1" msg:"field.platform_query_invalid"`
}

// PlatformWorkspaceUsageListInput 定义业务使用工作区列表的统计范围、排序与分页，Sort 缺省按服务周期数。
type PlatformWorkspaceUsageListInput struct {
	Days     int               `json:"days" query:"days,default=30" validate:"min=1" msg:"field.platform_query_invalid"`
	Sort     PlatformUsageSort `json:"sort" query:"sort,default=service_sessions" validate:"omitempty,oneof=service_sessions conversations first_response knowledge_gaps model_tokens" msg:"field.platform_query_invalid"`
	Page     int               `json:"page" query:"page,default=1"`
	PageSize int               `json:"pageSize" query:"pageSize,default=50"`
}

// PlatformUsageMetrics 定义业务使用指标，口径与工作区的 AI 表现和团队表现报表一致：ServiceSessions 为已关闭周期数，Conversations 为其所属会话去重数；
// AIClosed 为其中 AI 员工接待过的周期数，AIResolved 与 HandedOff 为其中 AI 独立解决与发生过转人工的周期数；首响为按工作时间计的真人首响（秒），没有样本时为空；KnowledgeGaps 为全部待处理的待补知识条数。
// ModelCalls 为平台模型调用数，ModelCallsConcluded 为其中成功、失败与超时的调用数，ModelCallsFailed 为其中失败与超时的调用数；InputTokens 含命中缓存的 CachedInputTokens。
type PlatformUsageMetrics struct {
	ServiceSessions     int   `json:"serviceSessions"`
	Conversations       int   `json:"conversations"`
	AIClosed            int   `json:"aiClosed"`
	AIResolved          int   `json:"aiResolved"`
	HandedOff           int   `json:"handedOff"`
	FirstResponseMedian *int  `json:"firstResponseMedian"`
	FirstResponseP90    *int  `json:"firstResponseP90"`
	KnowledgeGaps       int   `json:"knowledgeGaps"`
	ModelCalls          int   `json:"modelCalls"`
	ModelCallsConcluded int   `json:"modelCallsConcluded"`
	ModelCallsFailed    int   `json:"modelCallsFailed"`
	InputTokens         int64 `json:"inputTokens"`
	CachedInputTokens   int64 `json:"cachedInputTokens"`
	OutputTokens        int64 `json:"outputTokens"`
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

// PlatformRuntimeStatus 定义服务端进程与后台任务各队列的运行状态；DelayedTasks 为推迟到指定时间执行或等待重试的任务数，NotifyQueueUsage 为 PostgreSQL 通知队列的使用比例（0 到 1）。
type PlatformRuntimeStatus struct {
	Servers          []PlatformServer    `json:"servers"`
	Queues           []PlatformTaskQueue `json:"queues"`
	DelayedTasks     int                 `json:"delayedTasks"`
	NotifyQueueUsage float64             `json:"notifyQueueUsage"`
}

// PlatformServer 定义一个服务端进程及其最近一次心跳；BusDriver 为消息总线的传输驱动（postgres 或 nats），BusConnected 为最近一次心跳时消息总线是否可用，
// BusMessageRate 与 BusFailures 为上一个心跳间隔内消息总线的发送速率（条/秒）与发送失败和丢弃的消息数，Online 表示 30 秒内有心跳，Config 为进程启动时的服务端配置。
type PlatformServer struct {
	ID             string               `json:"id"`
	StartedAt      time.Time            `json:"startedAt"`
	HeartbeatAt    time.Time            `json:"heartbeatAt"`
	Hostname       string               `json:"hostname"`
	Version        string               `json:"version"`
	BusDriver      string               `json:"busDriver"`
	BusConnected   bool                 `json:"busConnected"`
	BusMessageRate float64              `json:"busMessageRate"`
	BusFailures    int                  `json:"busFailures"`
	Online         bool                 `json:"online"`
	Config         PlatformServerConfig `json:"config"`
}

// PlatformServerConfig 定义服务端进程的启动配置与本机目录，只包含不含密码和地址凭据的字段；Listen 为监听地址与端口，HTTPSPort 为本服务器直接提供 HTTPS 的端口，为 0 表示由前面的代理提供 HTTPS，LogLevel 为控制台日志的输出级别，LocalDirectory 为部署未开启对象存储时写入文件的本地目录。
type PlatformServerConfig struct {
	Listen           string                       `json:"listen"`
	HTTPSPort        int                          `json:"httpsPort"`
	Database         PlatformServerDatabaseConfig `json:"database"`
	NATS             PlatformServerNATSConfig     `json:"nats"`
	LogLevel         string                       `json:"logLevel"`
	LocalDirectory   string                       `json:"localDirectory"`
	ClientsDirectory string                       `json:"clientsDirectory"`
}

// PlatformServerDatabaseConfig 定义 PostgreSQL 连接的地址、账号名、库名与 SSL 模式。
type PlatformServerDatabaseConfig struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	User    string `json:"user"`
	Name    string `json:"name"`
	SSLMode string `json:"sslMode"`
}

// PlatformServerNATSConfig 定义去掉凭据的 NATS 地址、命名空间与任务队列副本数。
type PlatformServerNATSConfig struct {
	URL       string `json:"url"`
	Namespace string `json:"namespace"`
	Replicas  int    `json:"replicas"`
}

// PlatformDeployment 定义整个部署共用的部署配置；Certificate 是部署地址的证书设置，CertificateStatus 是证书的当前状态，HTTPSServers 表示部署中有直接提供 HTTPS 的服务器在线，此时 HTTPS 部署地址使用该证书；BrandingLicensed 表示授权当前授予自定义品牌，未授予时部署品牌保存后不生效；HomePricing 表示产品首页展示价格区块，此时首页的自部署介绍设置生效。
type PlatformDeployment struct {
	Name              string                    `json:"name"`
	PublicURL         string                    `json:"publicURL"`
	Certificate       PlatformCertificate       `json:"certificate"`
	CertificateStatus PlatformCertificateStatus `json:"certificateStatus"`
	HTTPSServers      bool                      `json:"httpsServers"`
	TimeZone          string                    `json:"timeZone"`
	TelemetryEnabled  bool                      `json:"telemetryEnabled"`
	Storage           PlatformStorageSettings   `json:"storage"`
	Email             PlatformEmailSettings     `json:"email"`
	Branding          PlatformBrandingSettings  `json:"branding"`
	Home              PlatformHomeSettings      `json:"home"`
	BrandingLicensed  bool                      `json:"brandingLicensed"`
	HomePricing       bool                      `json:"homePricing"`
}

// PlatformDeploymentBasicsInput 定义部署名称、平台时区与上报开关的修改值。
type PlatformDeploymentBasicsInput struct {
	Name             string `json:"name"`
	TimeZone         string `json:"timeZone"`
	TelemetryEnabled bool   `json:"telemetryEnabled"`
}

// PlatformCertificateSource 表示部署地址的证书来源。
type PlatformCertificateSource string

const (
	PlatformCertificateSourceACME   PlatformCertificateSource = PlatformCertificateSource(domain.CertificateSourceACME)
	PlatformCertificateSourceUpload PlatformCertificateSource = PlatformCertificateSource(domain.CertificateSourceUpload)
)

// PlatformCertificate 定义部署地址的证书设置：Source 为 acme 时自动签发并续期，为 upload 时使用 PEM 证书链 Certificate 与私钥 PrivateKey；自动签发时两者为空。
type PlatformCertificate struct {
	Source      PlatformCertificateSource `json:"source"`
	Certificate string                    `json:"certificate"`
	PrivateKey  string                    `json:"privateKey"`
}

// PlatformCertificateStatus 定义部署地址证书的当前状态：Domains 为证书包含的域名，ExpiresAt 为到期时间，没有证书时两者为空；RenewalError 与 RenewalFailedAt 为最近一次自动签发失败的原因与时间。
type PlatformCertificateStatus struct {
	Domains         []string   `json:"domains"`
	ExpiresAt       *time.Time `json:"expiresAt"`
	RenewalError    string     `json:"renewalError"`
	RenewalFailedAt *time.Time `json:"renewalFailedAt"`
}

// PlatformAddressInput 定义部署地址与证书设置的修改值。
type PlatformAddressInput struct {
	PublicURL   string              `json:"publicURL"`
	Certificate PlatformCertificate `json:"certificate"`
}

// PlatformStorageSettings 定义对象存储配置：Enabled 为假时新文件写入各服务器的本地文件目录，已有文件按各自的存储位置读取。
type PlatformStorageSettings struct {
	Enabled         bool   `json:"enabled"`
	Endpoint        string `json:"endpoint"`
	PublicBaseURL   string `json:"publicBaseURL"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"accessKeyID"`
	SecretAccessKey string `json:"secretAccessKey"`
	ForcePathStyle  bool   `json:"forcePathStyle"`
}

// PlatformEmailSettings 定义 SMTP 邮件发送配置，Host 为空时关闭邮件发送；Security 为 starttls、tls 或 none。
type PlatformEmailSettings struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Security    string `json:"security"`
	FromAddress string `json:"fromAddress"`
}

// PlatformBrandingSettings 定义部署品牌：Names 按界面语言覆盖产品名称，SDKName 为网站嵌入脚本对象名，Icon 为替换网站图标的 PNG 图片，均为空时沿用构建品牌。
type PlatformBrandingSettings struct {
	Names   map[string]string `json:"names"`
	SDKName string            `json:"sdkName"`
	Icon    []byte            `json:"icon"`
}

// PlatformHomeSettings 定义产品首页配置：SelfHost 表示首页提供价格区块时展示自部署介绍，不提供价格区块时首页始终展示。
type PlatformHomeSettings struct {
	SelfHost bool `json:"selfHost"`
}

// PlatformDeploymentSummary 定义诊断信息中的部署配置，不含密码与密钥；HasIcon 表示配置了网站图标。
type PlatformDeploymentSummary struct {
	Name              string                    `json:"name"`
	PublicURL         string                    `json:"publicURL"`
	CertificateSource PlatformCertificateSource `json:"certificateSource"`
	CertificateStatus PlatformCertificateStatus `json:"certificateStatus"`
	Storage           PlatformStorageSummary    `json:"storage"`
	Email             PlatformEmailSummary      `json:"email"`
	Branding          PlatformBrandingSummary   `json:"branding"`
	Home              PlatformHomeSettings      `json:"home"`
}

// PlatformStorageSummary 定义诊断信息中不含密钥的对象存储配置。
type PlatformStorageSummary struct {
	Enabled        bool   `json:"enabled"`
	Endpoint       string `json:"endpoint"`
	PublicBaseURL  string `json:"publicBaseURL"`
	Region         string `json:"region"`
	Bucket         string `json:"bucket"`
	ForcePathStyle bool   `json:"forcePathStyle"`
}

// PlatformEmailSummary 定义诊断信息中不含账号密码的邮件发送配置，Enabled 为假表示未配置 SMTP 主机。
type PlatformEmailSummary struct {
	Enabled     bool   `json:"enabled"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Security    string `json:"security"`
	FromAddress string `json:"fromAddress"`
}

// PlatformBrandingSummary 定义诊断信息中的部署品牌，HasIcon 表示配置了网站图标。
type PlatformBrandingSummary struct {
	Names   map[string]string `json:"names"`
	SDKName string            `json:"sdkName"`
	HasIcon bool              `json:"hasIcon"`
}

// PlatformObjectStorageStatus 定义诊断信息中的对象存储检查结果：Enabled 为假表示文件写入服务器本地目录；Error 为存储桶检查失败的原因，可以访问时为空。
type PlatformObjectStorageStatus struct {
	Enabled bool   `json:"enabled"`
	Error   string `json:"error"`
}

// PlatformControlStatus 定义与 control 同步的结果：SyncedAt 为最近一次成功的时间，从未成功时为空；FailedAt 与 Error 为此后最近一次失败的时间与原因。
type PlatformControlStatus struct {
	SyncedAt *time.Time `json:"syncedAt"`
	FailedAt *time.Time `json:"failedAt"`
	Error    string     `json:"error"`
}

// PlatformTaskQueue 定义一个后台任务队列的运行概况：Waiting 为尚未开始执行的任务数，Running 为执行中的任务数，Failed 为近 7 天最终失败的任务数。
type PlatformTaskQueue struct {
	Queue   string `json:"queue"`
	Waiting int    `json:"waiting"`
	Running int    `json:"running"`
	Failed  int    `json:"failed"`
}

// PlatformDiagnostics 定义平台管理员导出的诊断信息：GeneratedAt 为生成时间，ExportedBy 为生成诊断信息并检查对象存储的服务端进程编号，InstalledAt 为平台安装时间；
// FailedTasks 为近 7 天最终失败的任务。
type PlatformDiagnostics struct {
	GeneratedAt   time.Time                   `json:"generatedAt"`
	ExportedBy    string                      `json:"exportedBy"`
	InstalledAt   time.Time                   `json:"installedAt"`
	Overview      PlatformOverview            `json:"overview"`
	License       License                     `json:"license"`
	Deployment    PlatformDeploymentSummary   `json:"deployment"`
	ObjectStorage PlatformObjectStorageStatus `json:"objectStorage"`
	Runtime       PlatformRuntimeStatus       `json:"runtime"`
	Database      PlatformDatabaseStatus      `json:"database"`
	FailedTasks   []PlatformDiagnosticTask    `json:"failedTasks"`
}

// PlatformDatabaseStatus 定义 PostgreSQL 服务端版本与已执行的最新迁移版本。
type PlatformDatabaseStatus struct {
	Version   string `json:"version"`
	Migration int64  `json:"migration"`
}

// PlatformDiagnosticTask 定义诊断信息中一次最终失败的后台任务；WorkspaceID 为所属工作区编号，平台级任务为空，Attempt 为已尝试次数，FailedAt 为最终失败的时间。
type PlatformDiagnosticTask struct {
	ID          string    `json:"id"`
	Action      string    `json:"action"`
	Queue       string    `json:"queue"`
	WorkspaceID *string   `json:"workspaceId"`
	Attempt     int       `json:"attempt"`
	Error       string    `json:"error"`
	FailedAt    time.Time `json:"failedAt"`
}

// PlatformFailedTaskListInput 定义失败任务列表的分页。
type PlatformFailedTaskListInput struct {
	Page     int `json:"page" query:"page,default=1"`
	PageSize int `json:"pageSize" query:"pageSize,default=50"`
}

// PlatformFailedTask 定义一次近 7 天内最终失败的后台任务；WorkspaceName 为所属工作区名称，平台级任务为空，Attempt 为已尝试次数，FailedAt 为最终失败的时间。
type PlatformFailedTask struct {
	ID            string    `json:"id"`
	Action        string    `json:"action"`
	Queue         string    `json:"queue"`
	WorkspaceName *string   `json:"workspaceName"`
	Attempt       int       `json:"attempt"`
	Error         string    `json:"error"`
	FailedAt      time.Time `json:"failedAt"`
}

// PlatformFailedTaskList 定义失败任务分页结果。
type PlatformFailedTaskList struct {
	Tasks []PlatformFailedTask `json:"tasks"`
	Page  PageInfo             `json:"page"`
}

// ServerLogLevel 表示服务端日志级别。
type ServerLogLevel string

const (
	ServerLogLevelDebug ServerLogLevel = "debug"
	ServerLogLevelInfo  ServerLogLevel = "info"
	ServerLogLevelWarn  ServerLogLevel = "warn"
	ServerLogLevelError ServerLogLevel = "error"
)

// PlatformServerLogListInput 定义服务端日志列表的游标、页大小与筛选条件：Cursor 为上一页返回的 NextCursor，为空时从最新的日志读起；MinLevel 为空表示全部级别；Entry 匹配业务入口方法或后台任务名称；其余条件为空时不筛选。
type PlatformServerLogListInput struct {
	Cursor      string         `json:"cursor" query:"cursor"`
	PageSize    int            `json:"pageSize" query:"pageSize,default=50"`
	MinLevel    ServerLogLevel `json:"minLevel" query:"minLevel" validate:"omitempty,oneof=debug info warn error" msg:"field.platform_query_invalid"`
	InstanceID  string         `json:"instanceId" query:"instanceId"`
	WorkspaceID string         `json:"workspaceId" query:"workspaceId"`
	Entry       string         `json:"entry" query:"entry"`
	TraceID     string         `json:"traceId" query:"traceId"`
}

// PlatformServerLog 定义一条服务端日志：InstanceID 为写入日志的服务端进程实例编号；TraceID、Operation、TaskRunID、Action、Queue、WorkspaceID 与 AccountID 取自日志作用域，WorkspaceName 为所属工作区名称；EventID 为上报 control 的错误事件编号；Attributes 为日志的其他属性。
type PlatformServerLog struct {
	ID            string            `json:"id"`
	OccurredAt    time.Time         `json:"occurredAt"`
	Level         ServerLogLevel    `json:"level"`
	InstanceID    string            `json:"instanceId"`
	Hostname      string            `json:"hostname"`
	Version       string            `json:"version"`
	Message       string            `json:"message"`
	TraceID       *string           `json:"traceId"`
	Operation     *string           `json:"operation"`
	TaskRunID     *string           `json:"taskRunId"`
	Action        *string           `json:"action"`
	Queue         *string           `json:"queue"`
	WorkspaceID   *string           `json:"workspaceId"`
	WorkspaceName *string           `json:"workspaceName"`
	AccountID     *string           `json:"accountId"`
	Error         *string           `json:"error"`
	EventID       *string           `json:"eventId"`
	Attributes    map[string]string `json:"attributes"`
}

// PlatformServerLogList 定义一页服务端日志；NextCursor 为空表示没有更早的日志。
type PlatformServerLogList struct {
	Logs       []PlatformServerLog `json:"logs"`
	NextCursor string              `json:"nextCursor"`
}

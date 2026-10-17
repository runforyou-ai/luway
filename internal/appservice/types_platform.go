package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// AccountStatus 表示平台内登录账号的状态。
type AccountStatus = domain.AccountStatus

// RegistrationPolicy 表示平台的账号注册策略。
type RegistrationPolicy = domain.RegistrationPolicy

// LicenseStatus 表示授权状态。
type LicenseStatus = domain.LicenseStatus

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

// License 定义部署标识、授权状态、授权编号、客户、签发与到期时间；有效授权解锁全部产品功能。
type License struct {
	DeploymentID string        `json:"deploymentId"`
	Status       LicenseStatus `json:"status"`
	LicenseID    string        `json:"licenseId"`
	Customer     string        `json:"customer"`
	IssuedAt     *time.Time    `json:"issuedAt"`
	ExpiresAt    *time.Time    `json:"expiresAt"`
	Licensed     bool          `json:"licensed"`
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


// PlatformAccountListInput 定义平台账号列表的筛选与分页条件，Status 缺省为有效账号。
type PlatformAccountListInput struct {
	WorkspaceID string        `json:"workspaceId" query:"workspaceId" validate:"omitempty,uuid" msg:"error.platform_workspace_not_found"`
	Query       string        `json:"query" query:"query"`
	Status      AccountStatus `json:"status" query:"status,default=active" validate:"oneof=active inactive" msg:"field.user_status_invalid"`
	Page        int           `json:"page" query:"page,default=1"`
	PageSize    int           `json:"pageSize" query:"pageSize,default=50"`
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

// PlatformServer 定义一个服务端进程及其最近一次心跳；BusDriver 为消息总线的传输驱动（nats），BusConnected 为最近一次心跳时消息总线是否可用，
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

// PlatformServerConfig 定义服务端进程的启动配置与本机目录，只包含不含密码和地址凭据的字段；Listen 为监听地址与端口，HTTPSPort 为本服务器直接提供 HTTPS 的端口，为 0 表示由前面的代理提供 HTTPS，LogLevel 为控制台日志的输出级别。
type PlatformServerConfig struct {
	Listen           string                       `json:"listen"`
	HTTPSPort        int                          `json:"httpsPort"`
	Database         PlatformServerDatabaseConfig `json:"database"`
	NATS             PlatformServerNATSConfig     `json:"nats"`
	LogLevel         string                       `json:"logLevel"`
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
	URL string `json:"url"`
}

// PlatformSite 定义站点设置：部署名称与时区、注册与创建策略、访问地址与证书、部署品牌；BrandingLicensed 为假时 Branding 为构建品牌默认值且不可通过保存修改。
type PlatformSite struct {
	Name                   string                    `json:"name"`
	TimeZone               string                    `json:"timeZone"`
	RegistrationPolicy     RegistrationPolicy        `json:"registrationPolicy"`
	WorkspaceCreationQuota int                       `json:"workspaceCreationQuota"`
	PublicURL              string                    `json:"publicURL"`
	Certificate            PlatformCertificate       `json:"certificate"`
	CertificateStatus      PlatformCertificateStatus `json:"certificateStatus"`
	HTTPSServers           bool                      `json:"httpsServers"`
	Branding               PlatformBrandingSettings  `json:"branding"`
	BrandingLicensed       bool                      `json:"brandingLicensed"`
}

// PlatformSiteInput 定义站点设置的修改值；上传证书的 Certificate 与 PrivateKey 为空时保留已存值；未授权时忽略 Branding。
type PlatformSiteInput struct {
	Name                   string                   `json:"name"`
	TimeZone               string                   `json:"timeZone"`
	RegistrationPolicy     RegistrationPolicy       `json:"registrationPolicy" validate:"oneof=open invitation_only" msg:"field.registration_policy_invalid"`
	WorkspaceCreationQuota int                      `json:"workspaceCreationQuota" validate:"gte=0"`
	PublicURL              string                   `json:"publicURL"`
	Certificate            PlatformCertificate      `json:"certificate"`
	Branding               PlatformBrandingSettings `json:"branding"`
}

// PlatformDeployment 定义对象存储与邮件发送配置，供系统设置中的存储与邮件页面使用。
type PlatformDeployment struct {
	Storage PlatformStorageSettings `json:"storage"`
	Email   PlatformEmailSettings   `json:"email"`
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

// PlatformStorageSettings 定义整个部署共用的 S3 上传接口、文件访问地址与凭据。
type PlatformStorageSettings struct {
	Endpoint        string `json:"endpoint"`
	PublicBaseURL   string `json:"publicBaseURL"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"accessKeyID"`
	SecretAccessKey string `json:"secretAccessKey"`
	ForcePathStyle  bool   `json:"forcePathStyle"`
}

// EmailProvider 表示系统邮件的接入方式。
type EmailProvider = domain.EmailProvider

// PlatformEmailSettings 定义邮件配置读取结果，密钥只返回是否已设置。
type PlatformEmailSettings struct {
	Enabled     bool                           `json:"enabled"`
	Provider    EmailProvider                  `json:"provider"`
	FromAddress string                         `json:"fromAddress"`
	FromName    string                         `json:"fromName"`
	SMTP        *PlatformEmailSMTPSettings     `json:"smtp"`
	SES         *PlatformEmailSESSettings      `json:"ses"`
	Mailgun     *PlatformEmailMailgunSettings  `json:"mailgun"`
	Postmark    *PlatformEmailPostmarkSettings `json:"postmark"`
	Resend      *PlatformEmailResendSettings   `json:"resend"`
}

// PlatformEmailSMTPSettings 定义 SMTP 的配置读取结果。
type PlatformEmailSMTPSettings struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Security    string `json:"security"`
	Username    string `json:"username"`
	PasswordSet bool   `json:"passwordSet"`
}

// PlatformEmailSESSettings 定义 SES 的配置读取结果。
type PlatformEmailSESSettings struct {
	Region             string `json:"region"`
	AccessKeyID        string `json:"accessKeyID"`
	SecretAccessKeySet bool   `json:"secretAccessKeySet"`
}

// PlatformEmailMailgunSettings 定义 Mailgun 的配置读取结果。
type PlatformEmailMailgunSettings struct {
	Domain    string `json:"domain"`
	Region    string `json:"region"`
	APIKeySet bool   `json:"apiKeySet"`
}

// PlatformEmailPostmarkSettings 定义 Postmark 的配置读取结果。
type PlatformEmailPostmarkSettings struct {
	MessageStream  string `json:"messageStream"`
	ServerTokenSet bool   `json:"serverTokenSet"`
}

// PlatformEmailResendSettings 定义 Resend 的配置读取结果。
type PlatformEmailResendSettings struct {
	APIKeySet bool `json:"apiKeySet"`
}

// PlatformEmailInput 定义邮件配置保存参数，空密钥保留同服务商且身份未变的已存值。
type PlatformEmailInput struct {
	Enabled     bool                        `json:"enabled"`
	Provider    EmailProvider               `json:"provider"`
	FromAddress string                      `json:"fromAddress"`
	FromName    string                      `json:"fromName"`
	SMTP        *PlatformEmailSMTPInput     `json:"smtp"`
	SES         *PlatformEmailSESInput      `json:"ses"`
	Mailgun     *PlatformEmailMailgunInput  `json:"mailgun"`
	Postmark    *PlatformEmailPostmarkInput `json:"postmark"`
	Resend      *PlatformEmailResendInput   `json:"resend"`
}

// PlatformEmailSMTPInput 定义 SMTP 的配置保存参数。
type PlatformEmailSMTPInput struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Security string `json:"security"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// PlatformEmailSESInput 定义 SES 的配置保存参数。
type PlatformEmailSESInput struct {
	Region          string `json:"region"`
	AccessKeyID     string `json:"accessKeyID"`
	SecretAccessKey string `json:"secretAccessKey"`
}

// PlatformEmailMailgunInput 定义 Mailgun 的配置保存参数。
type PlatformEmailMailgunInput struct {
	Domain string `json:"domain"`
	Region string `json:"region"`
	APIKey string `json:"apiKey"`
}

// PlatformEmailPostmarkInput 定义 Postmark 的配置保存参数。
type PlatformEmailPostmarkInput struct {
	MessageStream string `json:"messageStream"`
	ServerToken   string `json:"serverToken"`
}

// PlatformEmailResendInput 定义 Resend 的配置保存参数。
type PlatformEmailResendInput struct {
	APIKey string `json:"apiKey"`
}

// PlatformEmailTestInput 定义测试邮件的收件邮箱。
type PlatformEmailTestInput struct {
	To string `json:"to" validate:"required,email" msg:"field.email_invalid"`
}

// PlatformBrandingSettings 定义部署品牌：Names 按界面语言覆盖产品名称，SDKName 为网站嵌入脚本对象名，Icon 为替换网站图标的 PNG 图片，均为空时沿用构建品牌。
type PlatformBrandingSettings struct {
	Names   map[string]string `json:"names"`
	SDKName string            `json:"sdkName"`
	Icon    []byte            `json:"icon"`
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
}

// PlatformStorageSummary 定义诊断信息中不含密钥的对象存储配置。
type PlatformStorageSummary struct {
	Endpoint       string `json:"endpoint"`
	PublicBaseURL  string `json:"publicBaseURL"`
	Region         string `json:"region"`
	Bucket         string `json:"bucket"`
	ForcePathStyle bool   `json:"forcePathStyle"`
}

// PlatformEmailSummary 定义诊断中的邮件配置摘要。
type PlatformEmailSummary struct {
	Enabled     bool          `json:"enabled"`
	Provider    EmailProvider `json:"provider"`
	FromAddress string        `json:"fromAddress"`
	FromName    string        `json:"fromName"`
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

// DiagnosticBundle 是可下载的 ZIP 诊断包。
type DiagnosticBundle struct {
	Name    string `json:"name"`
	Content []byte `json:"content"`
}

// DiagnosticUploadInput 主动上传诊断包的请求体（当前无附加字段）。
type DiagnosticUploadInput struct{}

// StorageOriginAccess 与领域中的源站访问检测结果保持一致。
type StorageOriginAccess = domain.StorageOriginAccess

// PlatformStorageCheck 返回保存时的实际源站匿名读取结果。
type PlatformStorageCheck struct {
	OriginAccess StorageOriginAccess `json:"originAccess"`
}

// Package appservice 定义跨平台应用服务及其传输契约，本文件放会话、安装和跨领域共用的契约。
package appservice

import (
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// Locale 表示应用支持的本地化语言。
type Locale = domain.Locale

// CustomerLocale 表示面向客户的界面、系统话术与通知邮件支持的语言。
type CustomerLocale = domain.CustomerLocale

// SessionState 表示会话入口。
type SessionState string

const (
	SessionStateReady     SessionState = "ready"
	SessionStateLogin     SessionState = "login"
	SessionStateSetup     SessionState = "setup"
	SessionStateConnect   SessionState = "connect"
	SessionStateWorkspace SessionState = "workspace"
	// SessionStateUpgrade 表示原生端接口版本低于服务器接受的最低版本，需要升级客户端。
	SessionStateUpgrade SessionState = "upgrade"
)

// ErrorKind 表示业务失败种类。
type ErrorKind string

const (
	ErrorKindInvalid     ErrorKind = "invalid"
	ErrorKindNotFound    ErrorKind = "not_found"
	ErrorKindForbidden   ErrorKind = "forbidden"
	ErrorKindConflict    ErrorKind = "conflict"
	ErrorKindUnavailable ErrorKind = "unavailable"
	ErrorKindFailed      ErrorKind = "failed"
	// ErrorKindRateLimited 表示请求超出限速，RetryAfter 秒后可以重试。
	ErrorKindRateLimited ErrorKind = "rate_limited"
)

// Brand 定义界面展示的产品品牌。
type Brand struct {
	// Names 是按界面语言标签给出的产品名称，至少包含 en-US。
	Names map[string]string `json:"names"`
	// SDKName 是网站嵌入脚本在宿主页注册的全局对象名。
	SDKName string `json:"sdkName"`
	// LinkScheme 是唤起客户端的链接协议名，连接链接为 `<LinkScheme>://connect?server=<部署地址>`。
	LinkScheme string `json:"linkScheme"`
}

// WorkspaceHeader 是工作区级调用携带目标工作区编号的请求头。
const WorkspaceHeader = "X-Workspace"

// RequestMeta 携带一次应用服务调用的认证、目标工作区和本地化信息；WorkspaceID 经 WorkspaceHeader 传输。
type RequestMeta struct {
	Token       string `json:"token"`
	WorkspaceID string `json:"workspaceId"`
	Locale      Locale `json:"locale"`
	// ExecutorVersion 是执行器请求声明的操作原语版本，其他请求为空。
	ExecutorVersion string `json:"-"`
}

// InstallationStatus 定义部署名称、平台是否已完成首次安装、注册策略是否开放注册、平台使用的产品品牌、服务端接口版本和服务端接受的最低原生端接口版本。
type InstallationStatus struct {
	DeploymentName      string `json:"deploymentName"`
	Installed           bool   `json:"installed"`
	RegistrationOpen    bool   `json:"registrationOpen"`
	Brand               Brand  `json:"brand"`
	APIVersion          int    `json:"apiVersion"`
	MinClientAPIVersion int    `json:"minClientApiVersion"`
}

// ProductDocPageInput 定义要读取的文档语言目录（zh-cn 或 en）与页面路径，首页路径为空字符串。
type ProductDocPageInput struct {
	Locale string `json:"locale" query:"locale"`
	Path   string `json:"path" query:"path"`
}

// ProductDocPage 定义文档页面的标题、渲染后的正文 HTML 与完整文档中的访问路径。
type ProductDocPage struct {
	Title string `json:"title"`
	HTML  string `json:"html"`
	Path  string `json:"path"`
}

// InstallWorkspaceInput 定义首次安装输入：部署地址、平台管理员账号和第一个工作区。
type InstallWorkspaceInput struct {
	PublicURL     string `json:"publicURL"`
	WorkspaceName string `json:"workspaceName"`
	DisplayName   string `json:"displayName"`
	Email         string `json:"email"`
	Password      string `json:"password"`
	Locale        Locale `json:"locale"`
	TimeZone      string `json:"timeZone"`
}

// InstallWorkspaceResult 包含首次安装创建的工作区和平台管理员登录会话。
type InstallWorkspaceResult struct {
	Auth      Auth      `json:"auth"`
	Workspace Workspace `json:"workspace"`
}

// RegisterInput 定义注册本地账号的输入；InvitationToken 非空时按邀请注册，平台未开放注册也可注册受邀邮箱。
type RegisterInput struct {
	DisplayName     string `json:"displayName"`
	Email           string `json:"email"`
	Password        string `json:"password"`
	Locale          Locale `json:"locale"`
	TimeZone        string `json:"timeZone"`
	InvitationToken string `json:"invitationToken"`
}

// LoginInput 定义登录输入。
type LoginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Auth 包含登录账号和会话令牌。
type Auth struct {
	Account   Account   `json:"account"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Account 定义当前登录账号。
type Account struct {
	ID              string `json:"id"`
	Email           string `json:"email"`
	DisplayName     string `json:"displayName"`
	Locale          Locale `json:"locale"`
	TimeZone        string `json:"timeZone"`
	IsPlatformAdmin bool   `json:"isPlatformAdmin"`
}

// WorkspaceStatus 表示工作区状态：active 正常，suspended 已被平台管理员暂停。
type WorkspaceStatus string

const (
	WorkspaceStatusActive    WorkspaceStatus = WorkspaceStatus(domain.WorkspaceLifecycleActive)
	WorkspaceStatusSuspended WorkspaceStatus = WorkspaceStatus(domain.WorkspaceLifecycleSuspended)
)

// Workspace 定义账号加入的工作区；已暂停的工作区不能进入。
type Workspace struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Slug   string          `json:"slug"`
	Status WorkspaceStatus `json:"status"`
}

// WorkspaceAttention 定义账号在一个工作区中的提醒数量，口径与该工作区收件箱的提醒数量一致。
type WorkspaceAttention struct {
	WorkspaceID          string `json:"workspaceId"`
	AttentionUnreadCount int    `json:"attentionUnreadCount"`
	PendingCount         int    `json:"pendingCount"`
	PendingUnreadCount   int    `json:"pendingUnreadCount"`
}

// WorkspaceAttentionList 定义账号在各工作区的提醒数量。
type WorkspaceAttentionList struct {
	Items []WorkspaceAttention `json:"items"`
}

// WorkspaceList 定义账号可进入的全部工作区；CanCreate 表示平台创建策略和平台工作区上限是否允许账号再创建工作区。
type WorkspaceList struct {
	Items     []Workspace `json:"items"`
	CanCreate bool        `json:"canCreate"`
}

// WorkspaceInput 定义新建工作区的名称。
type WorkspaceInput struct {
	Name string `json:"name"`
}

// Identity 定义当前成员及其所在工作区。
type Identity struct {
	Workspace CurrentWorkspace `json:"workspace"`
	User      CurrentUser      `json:"user"`
}

// PageInfo 定义分页信息。
type PageInfo struct {
	Number int `json:"number"`
	Size   int `json:"size"`
	Total  int `json:"total"`
}

// WorkspaceURL 返回部署地址下进入工作区的 Web 地址。
func WorkspaceURL(publicURL, slug string) string {
	return strings.TrimRight(publicURL, "/") + domain.WebAppPath + "#/w/" + slug
}

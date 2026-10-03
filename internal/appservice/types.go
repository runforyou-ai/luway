// Package appservice 定义跨平台应用服务及其传输契约。
//
// 各业务领域的传输类型按领域拆分在 types_*.go 中，本文件只保留
// 会话、安装和跨领域共用的通用契约。
package appservice

import (
	"strings"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// Locale 表示应用支持的本地化语言。
type Locale string

const (
	LocaleChineseSimplified   Locale = Locale(domain.LocaleChineseSimplified)
	LocaleEnglishUnitedStates Locale = Locale(domain.LocaleEnglishUnitedStates)
)

// CustomerLocale 表示面向客户的界面、系统话术与通知邮件支持的语言。
type CustomerLocale string

const (
	CustomerLocaleChineseSimplified   CustomerLocale = CustomerLocale(domain.CustomerLocaleChineseSimplified)
	CustomerLocaleEnglishUnitedStates CustomerLocale = CustomerLocale(domain.CustomerLocaleEnglishUnitedStates)
	CustomerLocaleHindiIndia          CustomerLocale = CustomerLocale(domain.CustomerLocaleHindiIndia)
)

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
)

// NotificationPermissionStatus 表示当前设备的系统通知授权状态。
type NotificationPermissionStatus string

const (
	NotificationPermissionStatusPrompt      NotificationPermissionStatus = "prompt"
	NotificationPermissionStatusGranted     NotificationPermissionStatus = "granted"
	NotificationPermissionStatusDenied      NotificationPermissionStatus = "denied"
	NotificationPermissionStatusUnsupported NotificationPermissionStatus = "unsupported"
)

// ClientUpdateState 表示原生端从当前服务器更新客户端的状态。
type ClientUpdateState string

const (
	// ClientUpdateStateUnsupported 表示当前端不能从服务器更新自身。
	ClientUpdateStateUnsupported ClientUpdateState = "unsupported"
	// ClientUpdateStateCurrent 表示服务器没有提供比当前客户端更新的版本。
	ClientUpdateStateCurrent ClientUpdateState = "current"
	// ClientUpdateStateReady 表示新版本已下载并通过签名校验，重启后生效。
	ClientUpdateStateReady ClientUpdateState = "ready"
	// ClientUpdateStateAvailable 表示服务器提供更新的版本，但当前端不能替换自身，需从服务器下载页安装。
	ClientUpdateStateAvailable ClientUpdateState = "available"
)

// ClientUpdate 是原生端从当前服务器更新客户端的结果。
type ClientUpdate struct {
	State ClientUpdateState `json:"state"`
	// Version 是服务器提供的新版本，只在 State 为 ready 或 available 时有值。
	Version string `json:"version"`
}

// ConnectReason 表示原生端已保存服务器仍进入连接页的原因。
type ConnectReason string

const (
	// ConnectReasonUnreachable 表示已保存的服务器暂时无法访问。
	ConnectReasonUnreachable ConnectReason = "unreachable"
	// ConnectReasonNotInstalled 表示已保存的服务器尚未完成首次安装。
	ConnectReasonNotInstalled ConnectReason = "not_installed"
	// ConnectReasonServerOutdated 表示已保存的服务器接口版本低于原生端要求的最低版本。
	ConnectReasonServerOutdated ConnectReason = "server_outdated"
)

// Startup 表示应用启动入口和界面使用的产品品牌；原生端已保存服务器仍进入连接页时 ConnectReason 说明原因。
type Startup struct {
	State         SessionState  `json:"state"`
	Brand         Brand         `json:"brand"`
	ConnectReason ConnectReason `json:"connectReason,omitempty"`
}

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

// InstallWorkspaceInput 定义首次安装输入：平台管理员账号和第一个工作区。
type InstallWorkspaceInput struct {
	WorkspaceName string `json:"workspaceName"`
	WorkspaceSlug string `json:"workspaceSlug"`
	DisplayName   string `json:"displayName"`
	Email         string `json:"email"`
	Password      string `json:"password"`
	Locale        Locale `json:"locale"`
	TimeZone      string `json:"timeZone"`
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
	WorkspaceStatusActive    WorkspaceStatus = WorkspaceStatus(domain.OrganizationLifecycleActive)
	WorkspaceStatusSuspended WorkspaceStatus = WorkspaceStatus(domain.OrganizationLifecycleSuspended)
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

// WorkspaceInput 定义新建工作区的名称和标识。
type WorkspaceInput struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Identity 定义当前成员及其所在工作区。
type Identity struct {
	Organization Organization `json:"organization"`
	User         CurrentUser  `json:"user"`
}

// ConversationWindowInput 定义桌面端打开会话独立窗口的输入，WorkspaceSlug 是会话所在工作区的标识。
type ConversationWindowInput struct {
	WorkspaceSlug  string `json:"workspaceSlug"`
	ConversationID string `json:"conversationId"`
	Title          string `json:"title"`
}

// MessageNotificationInput 定义当前设备的新消息通知内容；Path 是点击通知后打开的工作区页面地址（`/w/<工作区标识>/...`），为空时只把应用带到前台。
type MessageNotificationInput struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	SoundEnabled bool   `json:"soundEnabled"`
	Path         string `json:"path"`
}

// NotificationOpenedEventName 是原生端通知被点击后通知界面的 Wails 事件名，事件不携带数据，主界面收到后读取并清除待打开的页面地址。
const NotificationOpenedEventName = "app:notification:opened"

// ServerLinkOpenedEventName 是原生端被连接链接唤起后通知界面的 Wails 事件名，事件不携带数据，主界面收到后读取并清除链接携带的部署地址。
const ServerLinkOpenedEventName = "app:server-link:opened"

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

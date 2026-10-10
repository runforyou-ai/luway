package appservice

import "context"

// AuthBackend 定义首次安装、登录会话、账号与工作区入口的业务调用。
type AuthBackend interface {
	// GetRealtimeConnection 返回当前账号的成员实时连接配置。
	//appservice:route GET /realtime/connection auth=account
	GetRealtimeConnection(context.Context, RequestMeta) (RealtimeConnection, error)
	// InstallationStatus 返回部署名称、首次安装状态、是否开放注册、产品品牌和接口版本。
	//appservice:route GET /installation/status auth=public
	InstallationStatus(context.Context, RequestMeta) (InstallationStatus, error)
	// InstallWorkspace 在平台尚无账号时创建平台管理员和第一个工作区，返回工作区和平台管理员登录会话。
	//appservice:route POST /install status=201 auth=public
	InstallWorkspace(context.Context, RequestMeta, InstallWorkspaceInput) (InstallWorkspaceResult, error)
	// Login 校验账号密码并建立登录会话。
	//appservice:route POST /auth/login auth=public
	Login(context.Context, RequestMeta, LoginInput) (Auth, error)
	// Register 在平台开放注册或持有效邀请时注册本地账号并建立登录会话。
	//appservice:route POST /auth/register auth=public
	Register(context.Context, RequestMeta, RegisterInput) (Auth, error)
	// Logout 退出当前登录会话。
	//appservice:route POST /auth/logout auth=account
	Logout(context.Context, RequestMeta) error
	// LoadAccount 返回当前登录账号。
	//appservice:route GET /account auth=account
	LoadAccount(context.Context, RequestMeta) (Account, error)
	// ListWorkspaces 返回当前账号作为有效成员可进入的工作区，以及当前账号能否再创建工作区。
	//appservice:route GET /workspaces auth=account
	ListWorkspaces(context.Context, RequestMeta) (WorkspaceList, error)
	// ListWorkspaceAttention 返回当前账号在各工作区的提醒数量，工作区切换器与应用角标据此提示其他工作区的未读。
	//appservice:route GET /workspace-attention auth=account
	ListWorkspaceAttention(context.Context, RequestMeta) (WorkspaceAttentionList, error)
	// CreateWorkspace 创建工作区，当前账号成为首位管理员成员。
	//appservice:route POST /workspaces status=201 auth=account
	CreateWorkspace(context.Context, RequestMeta, WorkspaceInput) (Workspace, error)
	// LoadIdentity 返回当前账号在请求目标工作区中的成员身份。
	//appservice:route GET /auth/identity perm=none
	LoadIdentity(context.Context, RequestMeta) (Identity, error)
	// ChangePassword 核验当前账号的密码并保存新密码。
	//appservice:route PATCH /password auth=account
	ChangePassword(context.Context, RequestMeta, ChangePasswordInput) error
	// SetPushDevice 把本设备的离线推送目标记到当前登录会话，推送只发往未过期的登录会话。
	//appservice:route PUT /push-device auth=account
	SetPushDevice(context.Context, RequestMeta, PushDeviceInput) error
}

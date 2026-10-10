package appservice

import "context"

// PlatformBackend 定义平台概览、部署配置、平台账号与工作区、授权、用量与运行状态的平台管理调用。
type PlatformBackend interface {
	// GetPlatformOverview 返回平台规模与活跃趋势。
	//appservice:route GET /platform/overview auth=admin
	GetPlatformOverview(context.Context, RequestMeta) (PlatformOverview, error)
	// GetPlatformSettings 返回平台注册策略与工作区创建策略。
	//appservice:route GET /platform/settings auth=admin
	GetPlatformSettings(context.Context, RequestMeta) (PlatformSettings, error)
	// UpdatePlatformSettings 修改平台注册策略和工作区创建策略。
	//appservice:route PUT /platform/settings auth=admin
	UpdatePlatformSettings(context.Context, RequestMeta, PlatformPoliciesInput) (PlatformSettings, error)
	// GetPlatformDeployment 返回部署名称、部署地址、平台时区、上报开关、对象存储、邮件发送、部署品牌与产品首页配置。
	//appservice:route GET /platform/deployment auth=admin
	GetPlatformDeployment(context.Context, RequestMeta) (PlatformDeployment, error)
	// UpdatePlatformDeploymentBasics 修改部署名称、平台时区与上报开关，时区变化时在后台按新时区重建运营数据。
	//appservice:route PUT /platform/deployment/basics auth=admin
	UpdatePlatformDeploymentBasics(context.Context, RequestMeta, PlatformDeploymentBasicsInput) (PlatformDeployment, error)
	// UpdatePlatformAddress 修改部署地址与证书：上传的证书须与私钥匹配、未到期且包含 HTTPS 部署地址的域名；自动签发时，部署中有直接提供 HTTPS 的服务器而 HTTPS 部署地址没有有效证书时先签发证书，成功后一起保存。
	//appservice:route PUT /platform/deployment/address auth=admin
	UpdatePlatformAddress(context.Context, RequestMeta, PlatformAddressInput) (PlatformDeployment, error)
	// UpdatePlatformStorage 修改对象存储配置：开启时先确认能访问存储桶，部署中有其他服务器运行时不能关闭。
	//appservice:route PUT /platform/deployment/storage auth=admin
	UpdatePlatformStorage(context.Context, RequestMeta, PlatformStorageSettings) (PlatformDeployment, error)
	// UpdatePlatformEmail 修改 SMTP 邮件发送配置，主机为空时关闭邮件发送。
	//appservice:route PUT /platform/deployment/email auth=admin
	UpdatePlatformEmail(context.Context, RequestMeta, PlatformEmailSettings) (PlatformDeployment, error)
	// UpdatePlatformBranding 修改部署品牌，授权授予自定义品牌期间生效。
	//appservice:route PUT /platform/deployment/branding auth=admin
	UpdatePlatformBranding(context.Context, RequestMeta, PlatformBrandingSettings) (PlatformDeployment, error)
	// UpdatePlatformHome 修改产品首页配置，首页提供价格区块时生效。
	//appservice:route PUT /platform/deployment/home auth=admin
	UpdatePlatformHome(context.Context, RequestMeta, PlatformHomeSettings) (PlatformDeployment, error)
	// ListPlatformAccounts 返回平台内的账号。
	//appservice:route GET /platform/accounts auth=admin
	ListPlatformAccounts(context.Context, RequestMeta, PlatformAccountListInput) (PlatformAccountList, error)
	// DeactivatePlatformAccount 停用其他账号并使其登录会话失效。
	//appservice:route POST /platform/accounts/{accountID:uuid}/deactivate auth=admin
	DeactivatePlatformAccount(context.Context, RequestMeta, string) (PlatformAccount, error)
	// ReactivatePlatformAccount 恢复已停用的其他账号。
	//appservice:route POST /platform/accounts/{accountID:uuid}/reactivate auth=admin
	ReactivatePlatformAccount(context.Context, RequestMeta, string) (PlatformAccount, error)
	// GrantPlatformAdmin 把其他账号设为平台管理员。
	//appservice:route POST /platform/accounts/{accountID:uuid}/admin auth=admin
	GrantPlatformAdmin(context.Context, RequestMeta, string) (PlatformAccount, error)
	// RevokePlatformAdmin 撤销其他账号的平台管理员身份。
	//appservice:route DELETE /platform/accounts/{accountID:uuid}/admin auth=admin
	RevokePlatformAdmin(context.Context, RequestMeta, string) (PlatformAccount, error)
	// ListPlatformWorkspaces 返回平台内的全部工作区及其状态和当前规模。
	//appservice:route GET /platform/workspaces auth=admin
	ListPlatformWorkspaces(context.Context, RequestMeta, PlatformWorkspaceListInput) (PlatformWorkspaceList, error)
	// GetLicense 返回服务器标识、授权状态与 control 同步结果。
	//appservice:route GET /platform/license auth=admin
	GetLicense(context.Context, RequestMeta) (License, error)
	// ActivateLicense 用 control 签发的授权码离线激活或替换授权。
	//appservice:route PUT /platform/license auth=admin
	ActivateLicense(context.Context, RequestMeta, ActivateLicenseInput) (License, error)
	// ActivateLicenseOnline 用激活码经 control 在线激活授权。
	//appservice:route POST /platform/license/activations auth=admin
	ActivateLicenseOnline(context.Context, RequestMeta, ActivateLicenseOnlineInput) (License, error)
	// SyncLicense 立即向 control 登记服务器并拉取最新授权。
	//appservice:route POST /platform/license/sync auth=admin
	SyncLicense(context.Context, RequestMeta) (License, error)
	// SuspendPlatformWorkspace 暂停没有平台管理员成员的工作区：成员无法进入，渠道停止接待客户，后台任务挂起。
	//appservice:route POST /platform/workspaces/{workspaceID:uuid}/suspend auth=admin
	SuspendPlatformWorkspace(context.Context, RequestMeta, string) (PlatformWorkspace, error)
	// ResumePlatformWorkspace 恢复已暂停的工作区并重新执行挂起的后台任务。
	//appservice:route POST /platform/workspaces/{workspaceID:uuid}/resume auth=admin
	ResumePlatformWorkspace(context.Context, RequestMeta, string) (PlatformWorkspace, error)
	// GetPlatformUsage 返回平台整体最近若干天的客服业务使用指标与平台模型用量。
	//appservice:route GET /platform/usage auth=admin
	GetPlatformUsage(context.Context, RequestMeta, PlatformUsageInput) (PlatformUsageMetrics, error)
	// ListPlatformWorkspaceUsage 返回各工作区最近若干天的客服业务使用指标与平台模型用量。
	//appservice:route GET /platform/usage/workspaces auth=admin
	ListPlatformWorkspaceUsage(context.Context, RequestMeta, PlatformWorkspaceUsageListInput) (PlatformWorkspaceUsageList, error)
	// GetPlatformRuntimeStatus 返回服务端进程与后台任务各队列的运行状态。
	//appservice:route GET /platform/runtime auth=admin
	GetPlatformRuntimeStatus(context.Context, RequestMeta) (PlatformRuntimeStatus, error)
	// ListPlatformFailedTasks 返回等待重试与近 7 天内失败的后台任务。
	//appservice:route GET /platform/runtime/failed-tasks auth=admin
	ListPlatformFailedTasks(context.Context, RequestMeta, PlatformFailedTaskListInput) (PlatformFailedTaskList, error)
	// ListPlatformServerLogs 按筛选条件以时间倒序返回近 30 天的服务端日志，按游标逐页读取。
	//appservice:route GET /platform/runtime/server-logs auth=admin
	ListPlatformServerLogs(context.Context, RequestMeta, PlatformServerLogListInput) (PlatformServerLogList, error)
	// GetPlatformDiagnostics 返回平台概览、授权与 control 同步结果、部署配置与对象存储检查结果、各服务端进程的状态与配置、数据库、后台任务和平台供应商的诊断信息，不含密码、密钥与业务内容。
	//appservice:route GET /platform/diagnostics auth=admin
	GetPlatformDiagnostics(context.Context, RequestMeta) (PlatformDiagnostics, error)
}

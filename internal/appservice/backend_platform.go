package appservice

import "context"

// PlatformBackend 定义平台概览、部署配置、平台账号与工作区、授权、用量与运行状态的平台管理调用。
type PlatformBackend interface {
	// ExportDiagnosticBundle 生成当前服务端的诊断包。
	//appservice:route POST /platform/diagnostic-bundles auth=admin
	ExportDiagnosticBundle(context.Context, RequestMeta) (DiagnosticBundle, error)
	// UploadDiagnosticBundle 生成并主动上传诊断包。
	//appservice:route POST /platform/diagnostic-bundles/upload auth=admin
	UploadDiagnosticBundle(context.Context, RequestMeta, DiagnosticUploadInput) error

	// GetPlatformOverview 返回平台规模与活跃趋势。
	//appservice:route GET /platform/overview auth=admin
	GetPlatformOverview(context.Context, RequestMeta) (PlatformOverview, error)
	// GetPlatformSite 返回站点设置：部署名称与时区、注册与创建、访问地址与证书、部署品牌与产品首页。
	//appservice:route GET /platform/site auth=admin
	GetPlatformSite(context.Context, RequestMeta) (PlatformSite, error)
	// UpdatePlatformSite 一次保存站点设置；上传证书的空证书或私钥保留已存值；未授权时不改写品牌；时区变化时在后台按新时区重建运营数据；HTTPS 需自动签发时先签发再保存。
	//appservice:route PUT /platform/site auth=admin
	UpdatePlatformSite(context.Context, RequestMeta, PlatformSiteInput) (PlatformSite, error)
	// GetPlatformDeployment 返回对象存储与邮件发送配置。
	//appservice:route GET /platform/deployment auth=admin
	GetPlatformDeployment(context.Context, RequestMeta) (PlatformDeployment, error)
	// UpdatePlatformStorage 修改对象存储配置：开启时先确认能访问存储桶，部署中有其他服务器运行时不能关闭。
	//appservice:route PUT /platform/deployment/storage auth=admin
	UpdatePlatformStorage(context.Context, RequestMeta, PlatformStorageSettings) (PlatformStorageCheck, error)
	// UpdatePlatformEmail 修改当前邮件接入方式与发件人配置。
	//appservice:route PUT /platform/deployment/email auth=admin
	UpdatePlatformEmail(context.Context, RequestMeta, PlatformEmailInput) (PlatformDeployment, error)
	// TestPlatformEmail 使用已保存的配置发送测试邮件。
	//appservice:route POST /platform/deployment/email/test auth=admin
	TestPlatformEmail(context.Context, RequestMeta, PlatformEmailTestInput) error
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
	// GetLicense 返回部署标识、授权状态。
	//appservice:route GET /platform/license auth=admin
	GetLicense(context.Context, RequestMeta) (License, error)
	// ActivateLicense 用 control 签发的授权码离线激活或替换授权。
	//appservice:route PUT /platform/license auth=admin
	ActivateLicense(context.Context, RequestMeta, ActivateLicenseInput) (License, error)
	// ActivateLicenseOnline 用激活码经 control 在线激活授权。
	//appservice:route POST /platform/license/activations auth=admin
	ActivateLicenseOnline(context.Context, RequestMeta, ActivateLicenseOnlineInput) (License, error)
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
	// GetPlatformDiagnostics 返回平台概览、授权、部署配置与对象存储检查结果、各服务端进程的状态与配置、数据库、后台任务和平台供应商的诊断信息，不含密码、密钥与业务内容。
	//appservice:route GET /platform/diagnostics auth=admin
	GetPlatformDiagnostics(context.Context, RequestMeta) (PlatformDiagnostics, error)
}

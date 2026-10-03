//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"
	"time"

	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	platformdiagnosticsaction "github.com/runforyou-ai/luway/internal/actions/platformdiagnostics"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/license"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/control"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// platformOps 持有平台管理的 Action 和 Query。
type platformOps struct {
	platformOverview       *platformaction.OverviewQuery
	platformSettingsRead   *platformaction.SettingsQuery
	updatePlatformPolicies *platformaction.UpdatePoliciesAction
	updateTimeZone         *platformaction.UpdateTimeZoneAction
	listPlatformAccounts   *platformaction.ListAccountsQuery
	updatePlatformAccount  *platformaction.UpdateAccountAction
	listPlatformWorkspaces *platformaction.ListWorkspacesQuery
	setWorkspaceStatus     *platformaction.SetWorkspaceStatusAction
	platformUsage          *platformaction.UsageQuery
	platformRuntime        *platformaction.RuntimeStatusQuery
	platformFailedTasks    *platformaction.FailedTaskListQuery
	platformDiagnostics    *platformdiagnosticsaction.Query
	instanceID             string
	licenseRead            *platformaction.LicenseQuery
	activateLicense        *platformaction.ActivateLicenseAction
	onlineLicense          *platformaction.OnlineLicenseAction
	updateTelemetry        *platformaction.UpdateTelemetryAction
}

// newPlatformOps 创建平台管理的业务实现依赖，licenseKeys 是授权码验签公钥，controlClient 用于在线激活与同步授权，s3 是用于检查可用性的对象存储配置，telemetry 是上报开关缓存，instanceID 是本服务端进程编号。
func newPlatformOps(db *bun.DB, taskEnqueuer servertask.TxEnqueuer, licenseKeys license.Keys, controlClient *control.Client, s3 serverfilecontent.S3Config, telemetry *platformaction.Telemetry, instanceID string) platformOps {
	return platformOps{
		platformOverview:       platformaction.NewOverviewQuery(db),
		platformSettingsRead:   platformaction.NewSettingsQuery(db),
		updatePlatformPolicies: platformaction.NewUpdatePoliciesAction(db),
		updateTimeZone:         platformaction.NewUpdateTimeZoneAction(db, taskEnqueuer),
		listPlatformAccounts:   platformaction.NewListAccountsQuery(db),
		updatePlatformAccount:  platformaction.NewUpdateAccountAction(db),
		listPlatformWorkspaces: platformaction.NewListWorkspacesQuery(db),
		setWorkspaceStatus:     platformaction.NewSetWorkspaceStatusAction(db),
		platformUsage:          platformaction.NewUsageQuery(db),
		platformRuntime:        platformaction.NewRuntimeStatusQuery(db, s3),
		platformFailedTasks:    platformaction.NewFailedTaskListQuery(db),
		platformDiagnostics:    platformdiagnosticsaction.NewQuery(db, s3),
		instanceID:             instanceID,
		licenseRead:            platformaction.NewLicenseQuery(db),
		activateLicense:        platformaction.NewActivateLicenseAction(db, licenseKeys),
		onlineLicense:          platformaction.NewOnlineLicenseAction(db, licenseKeys, controlClient),
		updateTelemetry:        platformaction.NewUpdateTelemetryAction(db, telemetry),
	}
}

// GetPlatformOverview 返回服务器标识、规模、活跃趋势、授权状态和平台能力。
func (o *directOperations) GetPlatformOverview(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.PlatformOverview, error) {
	overview, err := o.platformOverview.Execute(ctx)
	if err != nil {
		return appservice.PlatformOverview{}, platformError(meta, err, i18n.ErrorPlatformOverviewFailed)
	}
	return platformOverviewFromAction(overview), nil
}

// GetPlatformSettings 返回平台注册策略、工作区创建策略、平台时区、运行指标与错误上报开关和每日赠送积分。
func (o *directOperations) GetPlatformSettings(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.PlatformSettings, error) {
	settings, err := o.platformSettingsRead.Execute(ctx)
	if err != nil {
		return appservice.PlatformSettings{}, platformError(meta, err, i18n.ErrorPlatformSettingsReadFailed)
	}
	return platformSettingsFromAction(settings), nil
}

// UpdatePlatformSettings 修改平台注册策略和工作区创建策略。
func (o *directOperations) UpdatePlatformSettings(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformPoliciesInput) (appservice.PlatformSettings, error) {
	settings, err := o.updatePlatformPolicies.Execute(ctx, account, platformaction.Policies{
		RegistrationPolicy:      domain.RegistrationPolicy(input.RegistrationPolicy),
		WorkspaceCreationPolicy: domain.WorkspaceCreationPolicy(input.WorkspaceCreationPolicy),
	})
	if err != nil {
		return appservice.PlatformSettings{}, platformError(meta, err, i18n.ErrorPlatformSettingsUpdateFailed)
	}
	slog.Info("平台策略已修改", "account_id", account.Account.ID,
		"registration_policy", settings.RegistrationPolicy, "workspace_creation_policy", settings.WorkspaceCreationPolicy)
	return platformSettingsFromAction(settings), nil
}

// UpdatePlatformTimeZone 修改平台时区，并按新时区在后台重建运营数据。
func (o *directOperations) UpdatePlatformTimeZone(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformTimeZoneInput) (appservice.PlatformSettings, error) {
	settings, err := o.updateTimeZone.Execute(ctx, account, input.TimeZone)
	if err != nil {
		return appservice.PlatformSettings{}, platformError(meta, err, i18n.ErrorPlatformSettingsUpdateFailed)
	}
	slog.Info("平台时区已修改", "account_id", account.Account.ID, "time_zone", settings.TimeZone)
	return platformSettingsFromAction(settings), nil
}

// UpdatePlatformTelemetry 开启或关闭向 control 上报运行指标与错误。
func (o *directOperations) UpdatePlatformTelemetry(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformTelemetryInput) (appservice.PlatformSettings, error) {
	settings, err := o.updateTelemetry.Execute(ctx, account, input.TelemetryEnabled)
	if err != nil {
		return appservice.PlatformSettings{}, platformError(meta, err, i18n.ErrorPlatformSettingsUpdateFailed)
	}
	slog.Info("上报开关已修改", "account_id", account.Account.ID, "telemetry_enabled", settings.TelemetryEnabled)
	return platformSettingsFromAction(settings), nil
}

// ListPlatformAccounts 返回平台内的账号。
func (o *directOperations) ListPlatformAccounts(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformAccountListInput) (appservice.PlatformAccountList, error) {
	output, err := o.listPlatformAccounts.Execute(ctx, platformaction.AccountListInput{
		Query: input.Query, Status: domain.AccountStatus(input.Status), Page: input.Page, PageSize: input.PageSize,
	})
	if err != nil {
		return appservice.PlatformAccountList{}, platformError(meta, err, i18n.ErrorPlatformAccountListFailed)
	}
	accounts := make([]appservice.PlatformAccount, 0, len(output.Accounts))
	for _, record := range output.Accounts {
		accounts = append(accounts, platformAccountFromAction(record))
	}
	return appservice.PlatformAccountList{Accounts: accounts, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}

// DeactivatePlatformAccount 停用其他账号并使其登录会话失效。
func (o *directOperations) DeactivatePlatformAccount(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, accountID string) (appservice.PlatformAccount, error) {
	record, err := o.updatePlatformAccount.SetStatus(ctx, account, accountID, domain.AccountStatusInactive)
	return platformAccountResult(meta, account, accountID, record, err, "账号已停用")
}

// ReactivatePlatformAccount 恢复已停用的其他账号。
func (o *directOperations) ReactivatePlatformAccount(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, accountID string) (appservice.PlatformAccount, error) {
	record, err := o.updatePlatformAccount.SetStatus(ctx, account, accountID, domain.AccountStatusActive)
	return platformAccountResult(meta, account, accountID, record, err, "账号已恢复")
}

// GrantPlatformAdmin 把其他账号设为平台管理员。
func (o *directOperations) GrantPlatformAdmin(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, accountID string) (appservice.PlatformAccount, error) {
	record, err := o.updatePlatformAccount.SetPlatformAdmin(ctx, account, accountID, true)
	return platformAccountResult(meta, account, accountID, record, err, "已设为平台管理员")
}

// RevokePlatformAdmin 撤销其他账号的平台管理员身份。
func (o *directOperations) RevokePlatformAdmin(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, accountID string) (appservice.PlatformAccount, error) {
	record, err := o.updatePlatformAccount.SetPlatformAdmin(ctx, account, accountID, false)
	return platformAccountResult(meta, account, accountID, record, err, "已撤销平台管理员")
}

// ListPlatformWorkspaces 返回平台内的全部工作区及其状态和当前规模。
func (o *directOperations) ListPlatformWorkspaces(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformWorkspaceListInput) (appservice.PlatformWorkspaceList, error) {
	output, err := o.listPlatformWorkspaces.Execute(ctx, platformaction.WorkspaceListInput{
		Query: input.Query, Status: domain.OrganizationLifecycleStatus(input.Status), Sort: platformaction.WorkspaceSort(input.Sort),
		Page: input.Page, PageSize: input.PageSize,
	})
	if err != nil {
		return appservice.PlatformWorkspaceList{}, platformError(meta, err, i18n.ErrorWorkspaceListFailed)
	}
	workspaces := make([]appservice.PlatformWorkspace, 0, len(output.Workspaces))
	for _, record := range output.Workspaces {
		workspaces = append(workspaces, platformWorkspaceFromAction(record))
	}
	return appservice.PlatformWorkspaceList{Workspaces: workspaces, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}

// GetLicense 返回服务器标识与授权状态。
func (o *directOperations) GetLicense(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.License, error) {
	current, err := o.licenseRead.Execute(ctx)
	if err != nil {
		return appservice.License{}, platformError(meta, err, i18n.ErrorLicenseReadFailed)
	}
	return licenseFromAction(current), nil
}

// ActivateLicense 用 control 签发的授权码离线激活或替换授权。
func (o *directOperations) ActivateLicense(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.ActivateLicenseInput) (appservice.License, error) {
	current, err := o.activateLicense.Execute(ctx, account, input.LicenseCode)
	if err != nil {
		return appservice.License{}, platformError(meta, err, i18n.ErrorLicenseActivateFailed)
	}
	slog.Info("授权已激活", "account_id", account.Account.ID, "license_id", current.LicenseID, "expires_at", current.ExpiresAt)
	return licenseFromAction(current), nil
}

// ActivateLicenseOnline 用激活码经 control 在线激活授权。
func (o *directOperations) ActivateLicenseOnline(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.ActivateLicenseOnlineInput) (appservice.License, error) {
	current, err := o.onlineLicense.Activate(ctx, account, input.ActivationCode)
	if err != nil {
		return appservice.License{}, platformError(meta, err, i18n.ErrorLicenseActivateFailed)
	}
	slog.Info("授权已在线激活", "account_id", account.Account.ID, "license_id", current.LicenseID, "expires_at", current.ExpiresAt)
	return licenseFromAction(current), nil
}

// SyncLicense 立即向 control 登记服务器并拉取最新授权。
func (o *directOperations) SyncLicense(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.License, error) {
	current, err := o.onlineLicense.Sync(ctx, account)
	if errors.Is(err, platformaction.ErrLicenseExpired) {
		return appservice.License{}, appservice.InvalidError(meta, i18n.ErrorLicenseRenewalRequired, nil)
	}
	if err != nil {
		return appservice.License{}, platformError(meta, err, i18n.ErrorLicenseSyncFailed)
	}
	slog.Info("授权已同步", "account_id", account.Account.ID, "license_id", current.LicenseID, "expires_at", current.ExpiresAt)
	return licenseFromAction(current), nil
}

// licenseFromAction 把授权状态转换为应用契约，未激活时不返回授权字段。
func licenseFromAction(current platformaction.License) appservice.License {
	output := appservice.License{
		ServerID: current.ServerID, Status: appservice.LicenseStatus(current.Status),
		Capabilities: appservice.Capabilities{
			WorkspaceLimit: current.Capabilities.WorkspaceLimit, CustomBranding: current.Capabilities.CustomBranding,
		},
	}
	if current.Status != domain.LicenseStatusNone {
		output.LicenseID, output.Customer = current.LicenseID, current.Customer
		output.IssuedAt, output.ExpiresAt = &current.IssuedAt, &current.ExpiresAt
		output.ControlMissingAt = current.ControlMissingAt
	}
	return output
}

// SuspendPlatformWorkspace 暂停没有平台管理员成员的工作区：成员无法进入，渠道停止接待客户，后台任务挂起。
func (o *directOperations) SuspendPlatformWorkspace(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, workspaceID string) (appservice.PlatformWorkspace, error) {
	return o.setPlatformWorkspaceStatus(ctx, meta, account, workspaceID, domain.OrganizationLifecycleSuspended, "工作区已暂停")
}

// ResumePlatformWorkspace 恢复已暂停的工作区并重新执行挂起的后台任务。
func (o *directOperations) ResumePlatformWorkspace(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, workspaceID string) (appservice.PlatformWorkspace, error) {
	return o.setPlatformWorkspaceStatus(ctx, meta, account, workspaceID, domain.OrganizationLifecycleActive, "工作区已恢复")
}

// setPlatformWorkspaceStatus 切换工作区状态并转换结果，成功时记录日志。
func (o *directOperations) setPlatformWorkspaceStatus(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, workspaceID string, status domain.OrganizationLifecycleStatus, message string) (appservice.PlatformWorkspace, error) {
	record, err := o.setWorkspaceStatus.Execute(ctx, account, workspaceID, status)
	if errors.Is(err, platformaction.ErrWorkspaceNotFound) {
		return appservice.PlatformWorkspace{}, appservice.NotFoundError(meta, i18n.ErrorPlatformWorkspaceNotFound)
	}
	if errors.Is(err, platformaction.ErrWorkspaceHasPlatformAdmin) {
		return appservice.PlatformWorkspace{}, appservice.InvalidError(meta, i18n.ErrorPlatformWorkspaceHasAdmin, nil)
	}
	if err != nil {
		return appservice.PlatformWorkspace{}, platformError(meta, err, i18n.ErrorPlatformWorkspaceUpdateFailed)
	}
	slog.Info(message, "operator_account_id", account.Account.ID, "workspace_id", workspaceID)
	return platformWorkspaceFromAction(record), nil
}

// GetPlatformUsage 返回平台整体最近若干天的客服业务使用指标与平台模型用量。
func (o *directOperations) GetPlatformUsage(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformUsageInput) (appservice.PlatformUsageMetrics, error) {
	metrics, err := o.platformUsage.Summary(ctx, input.Days)
	if err != nil {
		return appservice.PlatformUsageMetrics{}, platformError(meta, err, i18n.ErrorPlatformUsageFailed)
	}
	return appservice.PlatformUsageMetrics(metrics), nil
}

// ListPlatformWorkspaceUsage 返回各工作区最近若干天的客服业务使用指标与平台模型用量。
func (o *directOperations) ListPlatformWorkspaceUsage(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformWorkspaceUsageListInput) (appservice.PlatformWorkspaceUsageList, error) {
	output, err := o.platformUsage.ListWorkspaces(ctx, platformaction.UsageListInput{
		Days: input.Days, Sort: platformaction.UsageSort(input.Sort), Page: input.Page, PageSize: input.PageSize,
	})
	if err != nil {
		return appservice.PlatformWorkspaceUsageList{}, platformError(meta, err, i18n.ErrorPlatformUsageFailed)
	}
	workspaces := make([]appservice.PlatformWorkspaceUsage, 0, len(output.Workspaces))
	for _, record := range output.Workspaces {
		workspaces = append(workspaces, appservice.PlatformWorkspaceUsage{
			ID: record.ID, Name: record.Name, Slug: record.Slug, Status: appservice.WorkspaceStatus(record.Status),
			Metrics: appservice.PlatformUsageMetrics(record.UsageMetrics),
		})
	}
	return appservice.PlatformWorkspaceUsageList{
		Workspaces: workspaces,
		Page:       appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total},
	}, nil
}

// GetPlatformRuntimeStatus 返回服务端进程、外部依赖与后台任务各队列的运行状态。
func (o *directOperations) GetPlatformRuntimeStatus(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.PlatformRuntimeStatus, error) {
	runtime, err := o.platformRuntime.Execute(ctx)
	if err != nil {
		return appservice.PlatformRuntimeStatus{}, platformError(meta, err, i18n.ErrorPlatformRuntimeFailed)
	}
	return platformRuntimeFromAction(runtime), nil
}

// ListPlatformFailedTasks 返回等待重试与近 7 天内失败的后台任务。
func (o *directOperations) ListPlatformFailedTasks(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformFailedTaskListInput) (appservice.PlatformFailedTaskList, error) {
	output, err := o.platformFailedTasks.Execute(ctx, platformaction.FailedTaskListInput{Page: input.Page, PageSize: input.PageSize})
	if err != nil {
		return appservice.PlatformFailedTaskList{}, platformError(meta, err, i18n.ErrorPlatformRuntimeFailed)
	}
	tasks := make([]appservice.PlatformFailedTask, 0, len(output.Tasks))
	for _, task := range output.Tasks {
		tasks = append(tasks, appservice.PlatformFailedTask{
			ID: task.ID, Action: task.ActionName, Queue: task.QueueName, WorkspaceName: task.WorkspaceName, Retrying: task.Retrying,
			Attempt: task.Attempt, MaxAttempts: task.MaxAttempts, Error: task.LastError, FailedAt: task.FailedAt,
		})
	}
	return appservice.PlatformFailedTaskList{
		Tasks: tasks, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total},
	}, nil
}

// GetPlatformDiagnostics 返回平台概览、各服务端进程的状态与配置、外部依赖、数据库、后台任务和平台供应商的诊断信息，不含密码、密钥与业务内容。
func (o *directOperations) GetPlatformDiagnostics(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.PlatformDiagnostics, error) {
	diagnostics, err := o.platformDiagnostics.Execute(ctx)
	if err != nil {
		return appservice.PlatformDiagnostics{}, platformError(meta, err, i18n.ErrorPlatformDiagnosticsFailed)
	}
	output := appservice.PlatformDiagnostics{
		GeneratedAt: diagnostics.GeneratedAt, ExportedBy: o.instanceID,
		Overview: platformOverviewFromAction(diagnostics.Overview), Runtime: platformRuntimeFromAction(diagnostics.Runtime),
		Database:    appservice.PlatformDatabaseStatus(diagnostics.Database),
		FailedTasks: make([]appservice.PlatformDiagnosticTask, 0, len(diagnostics.FailedTasks)),
		AIProviders: make([]appservice.PlatformAIProviderSummary, 0, len(diagnostics.AIProviders)),
	}
	for _, task := range diagnostics.FailedTasks {
		output.FailedTasks = append(output.FailedTasks, appservice.PlatformDiagnosticTask{
			ID: task.ID, Action: task.ActionName, Queue: task.QueueName, WorkspaceID: task.OrganizationID, Retrying: task.Retrying,
			Attempt: task.Attempt, MaxAttempts: task.MaxAttempts, Error: task.LastError, FailedAt: task.FailedAt,
		})
	}
	for _, provider := range diagnostics.AIProviders {
		output.AIProviders = append(output.AIProviders, platformAIProviderSummaryFromAction(provider))
	}
	slog.Info("平台诊断信息已导出", "account_id", account.Account.ID)
	return output, nil
}

// platformOverviewFromAction 把平台概览转换为应用契约。
func platformOverviewFromAction(overview platformaction.Overview) appservice.PlatformOverview {
	trend := make([]appservice.PlatformDailyActivity, 0, len(overview.Trend))
	for _, day := range overview.Trend {
		trend = append(trend, appservice.PlatformDailyActivity{
			Date: day.Date.Format(time.DateOnly), ActiveAccounts: day.ActiveAccounts, ActiveWorkspaces: day.ActiveWorkspaces,
			NewAccounts: day.NewAccounts, NewWorkspaces: day.NewWorkspaces,
		})
	}
	return appservice.PlatformOverview{
		ServerID: overview.ServerID, InstalledAt: overview.InstalledAt, TimeZone: overview.TimeZone,
		StatsRebuilding: overview.StatsRebuilding,
		AccountCount:    overview.AccountCount, WorkspaceCount: overview.WorkspaceCount, MemberCount: overview.MemberCount,
		Last7Days: appservice.PlatformActivityWindow(overview.Last7Days), Last30Days: appservice.PlatformActivityWindow(overview.Last30Days),
		Trend: trend, License: licenseFromAction(overview.License),
		Capabilities: appservice.Capabilities{
			WorkspaceLimit: overview.Capabilities.WorkspaceLimit, CustomBranding: overview.Capabilities.CustomBranding,
		},
	}
}

// platformRuntimeFromAction 把平台运行状态转换为应用契约。
func platformRuntimeFromAction(runtime platformaction.RuntimeStatus) appservice.PlatformRuntimeStatus {
	status := appservice.PlatformRuntimeStatus{
		Servers:       make([]appservice.PlatformServer, 0, len(runtime.Servers)),
		ObjectStorage: appservice.PlatformObjectStorageStatus(runtime.ObjectStorage),
		Control:       appservice.PlatformControlStatus(runtime.Control),
		Queues:        make([]appservice.PlatformTaskQueue, 0, len(runtime.Queues)),
	}
	for _, server := range runtime.Servers {
		config := server.Config
		status.Servers = append(status.Servers, appservice.PlatformServer{
			ID: server.ID, StartedAt: server.StartedAt, HeartbeatAt: server.HeartbeatAt, Hostname: server.Hostname, Version: server.Version,
			TasksNATSConnected: server.TasksNATSConnected, RealtimeNATSConnected: server.RealtimeNATSConnected, Online: server.Online,
			Config: appservice.PlatformServerConfig{
				DeploymentName: config.DeploymentName, PublicURL: config.PublicURL, Listen: config.Listen, TLSMode: config.TLSMode,
				Database:         appservice.PlatformServerDatabaseConfig(config.Database),
				NATS:             appservice.PlatformServerNATSConfig(config.NATS),
				Storage:          appservice.PlatformServerStorageConfig(config.Storage),
				SMTP:             appservice.PlatformServerSMTPConfig(config.SMTP),
				ClientsDirectory: config.ClientsDir,
			},
		})
	}
	for _, queue := range runtime.Queues {
		status.Queues = append(status.Queues, appservice.PlatformTaskQueue(queue))
	}
	return status
}

// platformWorkspaceFromAction 把平台工作区记录转换为应用契约。
func platformWorkspaceFromAction(record platformaction.WorkspaceRecord) appservice.PlatformWorkspace {
	var lastActiveOn *string
	if record.LastActiveOn != nil {
		value := record.LastActiveOn.Format(time.DateOnly)
		lastActiveOn = &value
	}
	return appservice.PlatformWorkspace{
		ID: record.ID, Name: record.Name, Slug: record.Slug, Status: appservice.WorkspaceStatus(record.Status),
		MemberCount: record.MemberCount, AIEmployeeCount: record.AIEmployeeCount, ChannelCount: record.ChannelCount,
		ComputerCount: record.ComputerCount, HasPlatformAdmin: record.HasAdmin, StorageBytes: record.StorageBytes, LastActiveOn: lastActiveOn, CreatedAt: record.CreatedAt,
	}
}

// platformSettingsFromAction 把平台级策略转换为应用契约。
func platformSettingsFromAction(settings platformaction.Settings) appservice.PlatformSettings {
	return appservice.PlatformSettings{
		RegistrationPolicy:      appservice.RegistrationPolicy(settings.RegistrationPolicy),
		WorkspaceCreationPolicy: appservice.WorkspaceCreationPolicy(settings.WorkspaceCreationPolicy),
		TimeZone:                settings.TimeZone,
		TelemetryEnabled:        settings.TelemetryEnabled,
		DailyCreditGrant:        settings.DailyCreditGrant,
	}
}

// platformAccountFromAction 把平台账号记录转换为应用契约。
func platformAccountFromAction(record platformaction.AccountRecord) appservice.PlatformAccount {
	return appservice.PlatformAccount{
		ID: record.ID, Email: record.Email, DisplayName: record.DisplayName, Status: appservice.AccountStatus(record.Status),
		IsPlatformAdmin: record.IsPlatformAdmin, WorkspaceCount: record.WorkspaceCount, CreatedAt: record.CreatedAt,
	}
}

// platformAccountResult 把平台账号修改结果转换为应用契约，成功时记录日志。
func platformAccountResult(meta appservice.RequestMeta, account *servermodels.AccountIdentity, accountID string, record platformaction.AccountRecord, err error, message string) (appservice.PlatformAccount, error) {
	if err != nil {
		return appservice.PlatformAccount{}, platformError(meta, err, i18n.ErrorPlatformAccountUpdateFailed)
	}
	slog.Info(message, "operator_account_id", account.Account.ID, "account_id", accountID)
	return platformAccountFromAction(record), nil
}

// platformError 把平台管理操作的错误转换为本地化业务错误。
func platformError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		// 把平台管理校验错误码映射为本地化文案键。
		keys := map[common.FieldCode]i18n.Key{
			platformaction.ValidationQueryInvalid:                   i18n.FieldPlatformQueryInvalid,
			platformaction.ValidationAccountStatusInvalid:           i18n.FieldUserStatusInvalid,
			platformaction.ValidationRegistrationPolicyInvalid:      i18n.FieldRegistrationPolicyInvalid,
			platformaction.ValidationWorkspaceCreationPolicyInvalid: i18n.FieldWorkspaceCreationPolicyInvalid,
			platformaction.ValidationTimeZoneInvalid:                i18n.FieldTimeZoneInvalid,
			platformaction.ValidationWorkspaceSortInvalid:           i18n.FieldPlatformQueryInvalid,
			platformaction.ValidationWorkspaceStatusInvalid:         i18n.FieldPlatformQueryInvalid,
			platformaction.ValidationUsageSortInvalid:               i18n.FieldPlatformQueryInvalid,
			platformaction.ValidationUsageDaysInvalid:               i18n.FieldPlatformQueryInvalid,
		}
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, keys))
	}
	if errors.Is(err, platformaction.ErrNotPlatformAdmin) {
		return appservice.ForbiddenError(meta, i18n.ErrorPlatformAdminRequired)
	}
	if errors.Is(err, platformaction.ErrAccountNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorPlatformAccountNotFound)
	}
	if errors.Is(err, platformaction.ErrSelfChange) {
		return appservice.InvalidError(meta, i18n.ErrorPlatformAccountSelfChange, nil)
	}
	if errors.Is(err, platformaction.ErrNoActiveMembership) {
		return appservice.InvalidError(meta, i18n.ErrorPlatformAccountNoWorkspace, nil)
	}
	// 把授权码校验与 control 通信错误映射为本地化文案键。
	licenseErrors := map[error]i18n.Key{
		platformaction.ErrLicenseInvalid:           i18n.ErrorLicenseInvalid,
		platformaction.ErrLicenseServerMismatch:    i18n.ErrorLicenseServerMismatch,
		platformaction.ErrLicenseExpired:           i18n.ErrorLicenseExpired,
		platformaction.ErrLicenseSuperseded:        i18n.ErrorLicenseSuperseded,
		platformaction.ErrActivationCodeInvalid:    i18n.ErrorActivationCodeInvalid,
		platformaction.ErrActivationServerMismatch: i18n.ErrorActivationServerMismatch,
		platformaction.ErrServerKeyMismatch:        i18n.ErrorServerKeyMismatch,
		platformaction.ErrLicenseNotIssued:         i18n.ErrorLicenseNotIssued,
		platformaction.ErrControlUnavailable:       i18n.ErrorControlUnavailable,
	}
	for target, key := range licenseErrors {
		if errors.Is(err, target) {
			return appservice.InvalidError(meta, key, nil)
		}
	}
	return appservice.FailedError(meta, failureKey, err)
}

//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"
	"time"

	certificateaction "github.com/runforyou-ai/luway/internal/actions/certificate"
	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	licenseaction "github.com/runforyou-ai/luway/internal/actions/license"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	platformdiagnosticsaction "github.com/runforyou-ai/luway/internal/actions/platformdiagnostics"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/license"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/control"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// platformOps 持有平台管理的 Action 和 Query。
type platformOps struct {
	platformOverview       *platformaction.OverviewQuery
	platformSettingsRead   *platformaction.SettingsQuery
	updatePlatformPolicies *platformaction.UpdatePoliciesAction
	listPlatformAccounts   *platformaction.ListAccountsQuery
	updatePlatformAccount  *platformaction.UpdateAccountAction
	listPlatformWorkspaces *platformaction.ListWorkspacesQuery
	setWorkspaceStatus     *platformaction.SetWorkspaceStatusAction
	platformUsage          *platformaction.UsageQuery
	platformRuntime        *platformaction.RuntimeStatusQuery
	platformFailedTasks    *platformaction.FailedTaskListQuery
	platformServerLogs     *platformaction.ServerLogListQuery
	platformDiagnostics    *platformdiagnosticsaction.Query
	instanceID             string
	licenseRead            *licenseaction.LicenseQuery
	activateLicense        *licenseaction.ActivateLicenseAction
	onlineLicense          *licenseaction.OnlineLicenseAction
	deploymentRead         *deploymentaction.DeploymentSettingsQuery
	updateDeployment       *deploymentaction.UpdateDeploymentAction
	// homePricing 表示产品首页展示价格区块。
	homePricing bool
}

// newPlatformOps 创建平台管理的业务实现依赖，licenseKeys 是授权码验签公钥，controlClient 用于在线激活与同步授权，deployment 是本进程的部署状态，certificates 为部署地址准备证书，instanceID 是本服务端进程编号，homePricing 表示产品首页展示价格区块。
func newPlatformOps(db *bun.DB, taskEnqueuer servertask.TxEnqueuer, taskMonitor servertask.Monitor, licenseKeys license.Keys, controlClient *control.Client, deployment *deploymentaction.DeploymentState, certificates *certificateaction.Certificates, instanceID string, homePricing bool) *platformOps {
	return &platformOps{
		homePricing:            homePricing,
		platformOverview:       platformaction.NewOverviewQuery(db),
		platformSettingsRead:   platformaction.NewSettingsQuery(db),
		updatePlatformPolicies: platformaction.NewUpdatePoliciesAction(db, deployment),
		listPlatformAccounts:   platformaction.NewListAccountsQuery(db),
		updatePlatformAccount:  platformaction.NewUpdateAccountAction(db),
		listPlatformWorkspaces: platformaction.NewListWorkspacesQuery(db),
		setWorkspaceStatus:     platformaction.NewSetWorkspaceStatusAction(db),
		platformUsage:          platformaction.NewUsageQuery(db),
		platformRuntime:        platformaction.NewRuntimeStatusQuery(db, taskMonitor),
		platformFailedTasks:    platformaction.NewFailedTaskListQuery(db, taskMonitor),
		platformServerLogs:     platformaction.NewServerLogListQuery(db),
		platformDiagnostics:    platformdiagnosticsaction.NewQuery(db, taskMonitor),
		instanceID:             instanceID,
		licenseRead:            licenseaction.NewLicenseQuery(db),
		activateLicense:        licenseaction.NewActivateLicenseAction(db, licenseKeys, deployment),
		onlineLicense:          licenseaction.NewOnlineLicenseAction(db, licenseKeys, controlClient, deployment),
		deploymentRead:         deploymentaction.NewDeploymentSettingsQuery(db),
		updateDeployment:       deploymentaction.NewUpdateDeploymentAction(db, deployment, taskEnqueuer, certificates),
	}
}

// GetPlatformOverview 返回平台规模与活跃趋势。
func (o *platformOps) GetPlatformOverview(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.PlatformOverview, error) {
	overview, err := o.platformOverview.Execute(ctx)
	if err != nil {
		return appservice.PlatformOverview{}, platformError(meta, err, i18n.ErrorPlatformOverviewFailed)
	}
	return platformOverviewFromAction(overview), nil
}

// GetPlatformSettings 返回平台注册策略与工作区创建策略。
func (o *platformOps) GetPlatformSettings(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.PlatformSettings, error) {
	settings, err := o.platformSettingsRead.Execute(ctx)
	if err != nil {
		return appservice.PlatformSettings{}, platformError(meta, err, i18n.ErrorPlatformSettingsReadFailed)
	}
	return platformSettingsFromAction(settings), nil
}

// UpdatePlatformSettings 修改平台注册策略和工作区创建策略。
func (o *platformOps) UpdatePlatformSettings(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformPoliciesInput) (appservice.PlatformSettings, error) {
	settings, err := o.updatePlatformPolicies.Execute(ctx, account, platformaction.Policies{
		RegistrationPolicy:      input.RegistrationPolicy,
		WorkspaceCreationPolicy: input.WorkspaceCreationPolicy,
	})
	if err != nil {
		return appservice.PlatformSettings{}, platformError(meta, err, i18n.ErrorPlatformSettingsUpdateFailed)
	}
	slog.InfoContext(ctx, "平台策略已修改", "registration_policy", settings.RegistrationPolicy, "workspace_creation_policy", settings.WorkspaceCreationPolicy)
	return platformSettingsFromAction(settings), nil
}

// ListPlatformAccounts 返回平台内的账号。
func (o *platformOps) ListPlatformAccounts(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformAccountListInput) (appservice.PlatformAccountList, error) {
	output, err := o.listPlatformAccounts.Execute(ctx, platformaction.AccountListInput{
		Query: input.Query, Status: input.Status, Page: input.Page, PageSize: input.PageSize,
	})
	if err != nil {
		return appservice.PlatformAccountList{}, platformError(meta, err, i18n.ErrorPlatformAccountListFailed)
	}
	accounts := arr.Map(output.Accounts, platformAccountFromAction)
	return appservice.PlatformAccountList{Accounts: accounts, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}

// DeactivatePlatformAccount 停用其他账号并使其登录会话失效。
func (o *platformOps) DeactivatePlatformAccount(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, accountID string) (appservice.PlatformAccount, error) {
	record, err := o.updatePlatformAccount.SetStatus(ctx, account, accountID, domain.AccountStatusInactive)
	return platformAccountResult(ctx, meta, accountID, record, err, "账号已停用")
}

// ReactivatePlatformAccount 恢复已停用的其他账号。
func (o *platformOps) ReactivatePlatformAccount(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, accountID string) (appservice.PlatformAccount, error) {
	record, err := o.updatePlatformAccount.SetStatus(ctx, account, accountID, domain.AccountStatusActive)
	return platformAccountResult(ctx, meta, accountID, record, err, "账号已恢复")
}

// GrantPlatformAdmin 把其他账号设为平台管理员。
func (o *platformOps) GrantPlatformAdmin(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, accountID string) (appservice.PlatformAccount, error) {
	record, err := o.updatePlatformAccount.SetPlatformAdmin(ctx, account, accountID, true)
	return platformAccountResult(ctx, meta, accountID, record, err, "已设为平台管理员")
}

// RevokePlatformAdmin 撤销其他账号的平台管理员身份。
func (o *platformOps) RevokePlatformAdmin(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, accountID string) (appservice.PlatformAccount, error) {
	record, err := o.updatePlatformAccount.SetPlatformAdmin(ctx, account, accountID, false)
	return platformAccountResult(ctx, meta, accountID, record, err, "已撤销平台管理员")
}

// ListPlatformWorkspaces 返回平台内的全部工作区及其状态和当前规模。
func (o *platformOps) ListPlatformWorkspaces(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformWorkspaceListInput) (appservice.PlatformWorkspaceList, error) {
	output, err := o.listPlatformWorkspaces.Execute(ctx, platformaction.WorkspaceListInput{
		Query: input.Query, Status: domain.WorkspaceLifecycleStatus(input.Status), Sort: platformaction.WorkspaceSort(input.Sort),
		Page: input.Page, PageSize: input.PageSize,
	})
	if err != nil {
		return appservice.PlatformWorkspaceList{}, platformError(meta, err, i18n.ErrorWorkspaceListFailed)
	}
	workspaces := arr.Map(output.Workspaces, platformWorkspaceFromAction)
	return appservice.PlatformWorkspaceList{Workspaces: workspaces, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}

// GetLicense 返回服务器标识、授权状态与 control 同步结果。
func (o *platformOps) GetLicense(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.License, error) {
	current, err := o.licenseRead.Execute(ctx)
	if err != nil {
		return appservice.License{}, platformError(meta, err, i18n.ErrorLicenseReadFailed)
	}
	return licenseFromAction(current), nil
}

// ActivateLicense 用 control 签发的授权码离线激活或替换授权，返回激活后重新读取的授权状态。
func (o *platformOps) ActivateLicense(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.ActivateLicenseInput) (appservice.License, error) {
	current, err := o.activateLicense.Execute(ctx, account, input.LicenseCode)
	if err != nil {
		return appservice.License{}, platformError(meta, err, i18n.ErrorLicenseActivateFailed)
	}
	slog.InfoContext(ctx, "授权已激活", "license_id", current.LicenseID, "expires_at", current.ExpiresAt)
	return o.GetLicense(ctx, meta, account)
}

// ActivateLicenseOnline 用激活码经 control 在线激活授权，返回激活后重新读取的授权状态。
func (o *platformOps) ActivateLicenseOnline(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.ActivateLicenseOnlineInput) (appservice.License, error) {
	current, err := o.onlineLicense.Activate(ctx, account, input.ActivationCode)
	if err != nil {
		return appservice.License{}, platformError(meta, err, i18n.ErrorLicenseActivateFailed)
	}
	slog.InfoContext(ctx, "授权已在线激活", "license_id", current.LicenseID, "expires_at", current.ExpiresAt)
	return o.GetLicense(ctx, meta, account)
}

// SyncLicense 立即向 control 登记服务器并拉取最新授权，返回同步后重新读取的授权状态。
func (o *platformOps) SyncLicense(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.License, error) {
	current, err := o.onlineLicense.Sync(ctx, account)
	if errors.Is(err, licenseaction.ErrLicenseExpired) {
		return appservice.License{}, appservice.InvalidError(meta, i18n.ErrorLicenseRenewalRequired, nil)
	}
	if err != nil {
		return appservice.License{}, platformError(meta, err, i18n.ErrorLicenseSyncFailed)
	}
	slog.InfoContext(ctx, "授权已同步", "license_id", current.LicenseID, "expires_at", current.ExpiresAt)
	return o.GetLicense(ctx, meta, account)
}

// licenseFromAction 把授权状态转换为应用契约，未激活时不返回授权字段。
func licenseFromAction(current licenseaction.License) appservice.License {
	output := appservice.License{
		ServerID: current.ServerID, Status: current.Status,
		Capabilities: appservice.Capabilities(current.EffectiveCapabilities()),
	}
	if current.Status != domain.LicenseStatusNone {
		output.LicenseID, output.Customer = current.LicenseID, current.Customer
		output.IssuedAt, output.ExpiresAt = &current.IssuedAt, &current.ExpiresAt
		output.ControlMissingAt = current.ControlMissingAt
	}
	output.Sync = appservice.PlatformControlStatus(current.Sync)
	return output
}

// SuspendPlatformWorkspace 暂停没有平台管理员成员的工作区：成员无法进入，渠道停止接待客户，后台任务挂起。
func (o *platformOps) SuspendPlatformWorkspace(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, workspaceID string) (appservice.PlatformWorkspace, error) {
	return o.setPlatformWorkspaceStatus(ctx, meta, account, workspaceID, domain.WorkspaceLifecycleSuspended, "工作区已暂停")
}

// ResumePlatformWorkspace 恢复已暂停的工作区并重新执行挂起的后台任务。
func (o *platformOps) ResumePlatformWorkspace(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, workspaceID string) (appservice.PlatformWorkspace, error) {
	return o.setPlatformWorkspaceStatus(ctx, meta, account, workspaceID, domain.WorkspaceLifecycleActive, "工作区已恢复")
}

// setPlatformWorkspaceStatus 切换工作区状态并转换结果，成功时记录日志。
func (o *platformOps) setPlatformWorkspaceStatus(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, workspaceID string, status domain.WorkspaceLifecycleStatus, message string) (appservice.PlatformWorkspace, error) {
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
	slog.InfoContext(logscope.WithWorkspace(ctx, workspaceID), message, "operator_account_id", account.Account.ID)
	return platformWorkspaceFromAction(record), nil
}

// GetPlatformUsage 返回平台整体最近若干天的客服业务使用指标与平台模型用量。
func (o *platformOps) GetPlatformUsage(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformUsageInput) (appservice.PlatformUsageMetrics, error) {
	metrics, err := o.platformUsage.Summary(ctx, input.Days)
	if err != nil {
		return appservice.PlatformUsageMetrics{}, platformError(meta, err, i18n.ErrorPlatformUsageFailed)
	}
	return appservice.PlatformUsageMetrics(metrics), nil
}

// ListPlatformWorkspaceUsage 返回各工作区最近若干天的客服业务使用指标与平台模型用量。
func (o *platformOps) ListPlatformWorkspaceUsage(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformWorkspaceUsageListInput) (appservice.PlatformWorkspaceUsageList, error) {
	output, err := o.platformUsage.ListWorkspaces(ctx, platformaction.UsageListInput{
		Days: input.Days, Sort: platformaction.UsageSort(input.Sort), Page: input.Page, PageSize: input.PageSize,
	})
	if err != nil {
		return appservice.PlatformWorkspaceUsageList{}, platformError(meta, err, i18n.ErrorPlatformUsageFailed)
	}
	workspaces := arr.Map(output.Workspaces, func(record platformaction.WorkspaceUsage) appservice.PlatformWorkspaceUsage {
		return appservice.PlatformWorkspaceUsage{
			ID: record.ID, Name: record.Name, Slug: record.Slug, Status: appservice.WorkspaceStatus(record.Status),
			Metrics: appservice.PlatformUsageMetrics(record.UsageMetrics),
		}
	})
	return appservice.PlatformWorkspaceUsageList{
		Workspaces: workspaces,
		Page:       appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total},
	}, nil
}

// GetPlatformRuntimeStatus 返回服务端进程与后台任务各队列的运行状态。
func (o *platformOps) GetPlatformRuntimeStatus(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.PlatformRuntimeStatus, error) {
	runtime, err := o.platformRuntime.Execute(ctx)
	if err != nil {
		return appservice.PlatformRuntimeStatus{}, platformError(meta, err, i18n.ErrorPlatformRuntimeFailed)
	}
	return platformRuntimeFromAction(runtime), nil
}

// ListPlatformFailedTasks 返回近 7 天内最终失败的后台任务。
func (o *platformOps) ListPlatformFailedTasks(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformFailedTaskListInput) (appservice.PlatformFailedTaskList, error) {
	output, err := o.platformFailedTasks.Execute(ctx, platformaction.FailedTaskListInput{Page: input.Page, PageSize: input.PageSize})
	if err != nil {
		return appservice.PlatformFailedTaskList{}, platformError(meta, err, i18n.ErrorPlatformRuntimeFailed)
	}
	tasks := arr.Map(output.Tasks, func(task platformaction.FailedTask) appservice.PlatformFailedTask {
		return appservice.PlatformFailedTask{
			ID: task.ID, Action: task.ActionName, Queue: task.QueueName, WorkspaceName: task.WorkspaceName,
			Attempt: task.Attempt, Error: task.LastError, FailedAt: task.FailedAt,
		}
	})
	return appservice.PlatformFailedTaskList{
		Tasks: tasks, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total},
	}, nil
}

// serverLogLevels 是日志级别对应的 slog 级别数值。
var serverLogLevels = map[appservice.ServerLogLevel]slog.Level{
	appservice.ServerLogLevelDebug: slog.LevelDebug,
	appservice.ServerLogLevelInfo:  slog.LevelInfo,
	appservice.ServerLogLevelWarn:  slog.LevelWarn,
	appservice.ServerLogLevelError: slog.LevelError,
}

// ListPlatformServerLogs 按筛选条件以时间倒序返回近 30 天的服务端日志，按游标逐页读取。
func (o *platformOps) ListPlatformServerLogs(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformServerLogListInput) (appservice.PlatformServerLogList, error) {
	query := platformaction.ServerLogListInput{
		Cursor: input.Cursor, PageSize: input.PageSize, InstanceID: input.InstanceID,
		WorkspaceID: input.WorkspaceID, Entry: input.Entry, TraceID: input.TraceID,
	}
	if input.MinLevel != "" {
		query.MinLevel = new(int(serverLogLevels[input.MinLevel]))
	}
	output, err := o.platformServerLogs.Execute(ctx, query)
	if err != nil {
		return appservice.PlatformServerLogList{}, platformError(meta, err, i18n.ErrorPlatformRuntimeFailed)
	}
	logs := make([]appservice.PlatformServerLog, 0, len(output.Logs))
	for _, record := range output.Logs {
		// 记录的级别数值归入不高于它的最高日志级别。
		level := appservice.ServerLogLevelDebug
		for candidate, value := range serverLogLevels {
			if slog.Level(record.Level) >= value && value > serverLogLevels[level] {
				level = candidate
			}
		}
		logs = append(logs, appservice.PlatformServerLog{
			ID: record.ID, OccurredAt: record.OccurredAt, Level: level, InstanceID: record.InstanceID, Hostname: record.Hostname,
			Version: record.Version, Message: record.Message, TraceID: record.TraceID, Operation: record.Operation, TaskRunID: record.TaskRunID,
			Action: record.Action, Queue: record.Queue, WorkspaceID: record.WorkspaceID, WorkspaceName: record.WorkspaceName,
			AccountID: record.AccountID, Error: record.Error, EventID: record.EventID, Attributes: record.Attributes,
		})
	}
	return appservice.PlatformServerLogList{Logs: logs, NextCursor: output.NextCursor}, nil
}

// GetPlatformDiagnostics 返回平台概览、授权与 control 同步结果、部署配置与对象存储检查结果、各服务端进程的状态与配置、数据库、后台任务和平台供应商的诊断信息，不含密码、密钥与业务内容。
func (o *platformOps) GetPlatformDiagnostics(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.PlatformDiagnostics, error) {
	diagnostics, err := o.platformDiagnostics.Execute(ctx)
	if err != nil {
		return appservice.PlatformDiagnostics{}, platformError(meta, err, i18n.ErrorPlatformDiagnosticsFailed)
	}
	output := appservice.PlatformDiagnostics{
		GeneratedAt: diagnostics.GeneratedAt, ExportedBy: o.instanceID, InstalledAt: diagnostics.InstalledAt,
		Overview: platformOverviewFromAction(diagnostics.Overview), License: licenseFromAction(diagnostics.License),
		ObjectStorage: appservice.PlatformObjectStorageStatus(diagnostics.ObjectStorage), Deployment: platformDeploymentSummaryFromAction(diagnostics.Deployment), Runtime: platformRuntimeFromAction(diagnostics.Runtime),
		Database: appservice.PlatformDatabaseStatus(diagnostics.Database),
		FailedTasks: arr.Map(diagnostics.FailedTasks, func(task servertask.FailedRun) appservice.PlatformDiagnosticTask {
			return appservice.PlatformDiagnosticTask{
				ID: task.ID, Action: task.ActionName, Queue: task.QueueName, WorkspaceID: task.WorkspaceID,
				Attempt: task.Attempt, Error: task.LastError, FailedAt: task.FailedAt,
			}
		}),
	}
	slog.InfoContext(ctx, "平台诊断信息已导出")
	return output, nil
}

// platformOverviewFromAction 把平台概览转换为应用契约。
func platformOverviewFromAction(overview platformaction.Overview) appservice.PlatformOverview {
	trend := arr.Map(overview.Trend, func(day platformaction.DailyActivity) appservice.PlatformDailyActivity {
		return appservice.PlatformDailyActivity{
			Date: day.Date.Format(time.DateOnly), ActiveAccounts: day.ActiveAccounts, ActiveWorkspaces: day.ActiveWorkspaces,
			NewAccounts: day.NewAccounts, NewWorkspaces: day.NewWorkspaces,
		}
	})
	return appservice.PlatformOverview{
		TimeZone: overview.TimeZone, StatsRebuilding: overview.StatsRebuilding,
		AccountCount: overview.AccountCount, WorkspaceCount: overview.WorkspaceCount, MemberCount: overview.MemberCount,
		Last7Days: appservice.PlatformActivityWindow(overview.Last7Days), Last30Days: appservice.PlatformActivityWindow(overview.Last30Days),
		Trend: trend,
	}
}

// platformRuntimeFromAction 把平台运行状态转换为应用契约。
func platformRuntimeFromAction(runtime platformaction.RuntimeStatus) appservice.PlatformRuntimeStatus {
	status := appservice.PlatformRuntimeStatus{
		Servers: make([]appservice.PlatformServer, 0, len(runtime.Servers)),
		Queues: arr.Map(runtime.Queues, func(queue servertask.QueueStatus) appservice.PlatformTaskQueue {
			return appservice.PlatformTaskQueue(queue)
		}),
		DelayedTasks:     runtime.DelayedTasks,
		NotifyQueueUsage: runtime.NotifyQueueUsage,
	}
	for _, server := range runtime.Servers {
		config := server.Config
		status.Servers = append(status.Servers, appservice.PlatformServer{
			ID: server.ID, StartedAt: server.StartedAt, HeartbeatAt: server.HeartbeatAt, Hostname: server.Hostname, Version: server.Version,
			BusDriver: server.BusDriver, BusConnected: server.BusConnected, BusMessageRate: server.BusMessageRate, BusFailures: server.BusFailures, Online: server.Online,
			Config: appservice.PlatformServerConfig{
				Listen: config.Listen, HTTPSPort: config.HTTPSPort,
				Database:         appservice.PlatformServerDatabaseConfig(config.Database),
				NATS:             appservice.PlatformServerNATSConfig(config.NATS),
				LogLevel:         config.LogLevel,
				LocalDirectory:   config.LocalDirectory,
				ClientsDirectory: config.ClientsDir,
			},
		})
	}
	return status
}

// platformWorkspaceFromAction 把平台工作区记录转换为应用契约。
func platformWorkspaceFromAction(record platformaction.WorkspaceRecord) appservice.PlatformWorkspace {
	return appservice.PlatformWorkspace{
		ID: record.ID, Name: record.Name, Slug: record.Slug, Status: appservice.WorkspaceStatus(record.Status),
		MemberCount: record.MemberCount, AIEmployeeCount: record.AIEmployeeCount, ChannelCount: record.ChannelCount,
		ComputerCount: record.ComputerCount, HasPlatformAdmin: record.HasAdmin, StorageBytes: record.StorageBytes, LastActiveDays: record.LastActiveDays, CreatedAt: record.CreatedAt,
	}
}

// platformSettingsFromAction 把平台级策略转换为应用契约。
func platformSettingsFromAction(settings platformaction.Settings) appservice.PlatformSettings {
	return appservice.PlatformSettings{
		RegistrationPolicy:      settings.RegistrationPolicy,
		WorkspaceCreationPolicy: settings.WorkspaceCreationPolicy,
	}
}

// platformAccountFromAction 把平台账号记录转换为应用契约。
func platformAccountFromAction(record platformaction.AccountRecord) appservice.PlatformAccount {
	return appservice.PlatformAccount{
		ID: record.ID, Email: record.Email, DisplayName: record.DisplayName, Status: appservice.AccountStatus(record.Status),
		IsPlatformAdmin: record.IsPlatformAdmin, WorkspaceCount: record.WorkspaceCount, CreatedAt: record.CreatedAt,
	}
}

// platformAccountResult 把平台账号修改结果转换为应用契约，成功时记录被修改的账号。
func platformAccountResult(ctx context.Context, meta appservice.RequestMeta, accountID string, record platformaction.AccountRecord, err error, message string) (appservice.PlatformAccount, error) {
	if err != nil {
		return appservice.PlatformAccount{}, platformError(meta, err, i18n.ErrorPlatformAccountUpdateFailed)
	}
	slog.InfoContext(ctx, message, "target_account_id", accountID)
	return platformAccountFromAction(record), nil
}

// platformFieldKeys 把平台管理校验错误码映射为本地化文案键。
var platformFieldKeys = map[common.FieldCode]i18n.Key{
	platformaction.ValidationQueryInvalid:                 i18n.FieldPlatformQueryInvalid,
	deploymentaction.ValidationTimeZoneInvalid:            i18n.FieldTimeZoneInvalid,
	deploymentaction.ValidationDeploymentNameInvalid:      i18n.FieldDeploymentNameInvalid,
	certificateaction.ValidationPublicURLInvalid:          i18n.FieldPublicURLInvalid,
	certificateaction.ValidationPublicURLDomainRequired:   i18n.FieldPublicURLDomainRequired,
	certificateaction.ValidationCertificateSourceInvalid:  i18n.FieldCertificateSourceInvalid,
	certificateaction.ValidationCertificateInvalid:        i18n.FieldCertificateInvalid,
	certificateaction.ValidationCertificateExpired:        i18n.FieldCertificateExpired,
	certificateaction.ValidationCertificateDomainMismatch: i18n.FieldCertificateDomainMismatch,
	deploymentaction.ValidationS3URLInvalid:               i18n.FieldHTTPURLInvalid,
	deploymentaction.ValidationS3SettingRequired:          i18n.FieldStorageSettingRequired,
	deploymentaction.ValidationSMTPPortInvalid:            i18n.FieldPortInvalid,
	deploymentaction.ValidationSMTPSecurityInvalid:        i18n.FieldSMTPSecurityInvalid,
	deploymentaction.ValidationSMTPCredentialsIncomplete:  i18n.FieldSMTPCredentialsIncomplete,
	deploymentaction.ValidationSMTPFromAddressInvalid:     i18n.FieldEmailInvalid,
	deploymentaction.ValidationBrandNameInvalid:           i18n.FieldBrandNameInvalid,
	deploymentaction.ValidationBrandSDKNameInvalid:        i18n.FieldBrandSDKNameInvalid,
	deploymentaction.ValidationBrandIconInvalid:           i18n.FieldBrandIconInvalid,
}

// platformErrors 是平台管理操作的错误转换规则，授权码校验与 control 通信错误按输入无效返回。
var platformErrors = dispatch.Catalog{
	dispatch.FieldRule(platformFieldKeys),
	dispatch.Is(platformaction.ErrNotPlatformAdmin, dispatch.Forbidden(i18n.ErrorPlatformAdminRequired)),
	dispatch.Is(platformaction.ErrAccountNotFound, dispatch.NotFound(i18n.ErrorPlatformAccountNotFound)),
	dispatch.Is(platformaction.ErrSelfChange, dispatch.Invalid(i18n.ErrorPlatformAccountSelfChange)),
	dispatch.Is(platformaction.ErrNoActiveMembership, dispatch.Invalid(i18n.ErrorPlatformAccountNoWorkspace)),
	dispatch.Is(licenseaction.ErrLicenseInvalid, dispatch.Invalid(i18n.ErrorLicenseInvalid)),
	dispatch.Is(licenseaction.ErrLicenseServerMismatch, dispatch.Invalid(i18n.ErrorLicenseServerMismatch)),
	dispatch.Is(licenseaction.ErrLicenseExpired, dispatch.Invalid(i18n.ErrorLicenseExpired)),
	dispatch.Is(licenseaction.ErrLicenseSuperseded, dispatch.Invalid(i18n.ErrorLicenseSuperseded)),
	dispatch.Is(licenseaction.ErrActivationCodeInvalid, dispatch.Invalid(i18n.ErrorActivationCodeInvalid)),
	dispatch.Is(licenseaction.ErrActivationServerMismatch, dispatch.Invalid(i18n.ErrorActivationServerMismatch)),
	dispatch.Is(licenseaction.ErrServerKeyMismatch, dispatch.Invalid(i18n.ErrorServerKeyMismatch)),
	dispatch.Is(licenseaction.ErrLicenseNotIssued, dispatch.Invalid(i18n.ErrorLicenseNotIssued)),
	dispatch.Is(licenseaction.ErrControlUnavailable, dispatch.Invalid(i18n.ErrorControlUnavailable)),
}

// platformError 把平台管理操作的错误转换为本地化业务错误。
func platformError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return platformErrors.Translate(meta, err, failureKey)
}

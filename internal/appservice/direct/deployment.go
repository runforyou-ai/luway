//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"
	"time"

	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	"github.com/runforyou-ai/luway/internal/common/license"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/control"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// deploymentOps 持有部署管理的 Action 和 Query。
type deploymentOps struct {
	deploymentOverview       *deploymentaction.OverviewQuery
	deploymentSettingsRead   *deploymentaction.SettingsQuery
	updateDeploymentPolicies *deploymentaction.UpdatePoliciesAction
	updateStatisticsTimeZone *deploymentaction.UpdateStatisticsTimeZoneAction
	listDeploymentAccounts   *deploymentaction.ListAccountsQuery
	updateDeploymentAccount  *deploymentaction.UpdateAccountAction
	listDeploymentWorkspaces *deploymentaction.ListWorkspacesQuery
	setWorkspaceStatus       *deploymentaction.SetWorkspaceStatusAction
	deploymentUsage          *deploymentaction.UsageQuery
	deploymentTaskQueues     *deploymentaction.TaskQueuesQuery
	deploymentFailedTasks    *deploymentaction.FailedTaskListQuery
	instanceLicenseRead      *deploymentaction.LicenseQuery
	activateInstanceLicense  *deploymentaction.ActivateLicenseAction
	onlineInstanceLicense    *deploymentaction.OnlineLicenseAction
	updateTelemetry          *deploymentaction.UpdateTelemetryAction
}

// newDeploymentOps 创建部署管理的业务实现依赖，licenseKeys 是授权码验签公钥，controlClient 用于在线激活与同步授权。
func newDeploymentOps(db *bun.DB, taskEnqueuer servertask.TxEnqueuer, licenseKeys license.Keys, controlClient *control.Client) deploymentOps {
	return deploymentOps{
		deploymentOverview:       deploymentaction.NewOverviewQuery(db),
		deploymentSettingsRead:   deploymentaction.NewSettingsQuery(db),
		updateDeploymentPolicies: deploymentaction.NewUpdatePoliciesAction(db),
		updateStatisticsTimeZone: deploymentaction.NewUpdateStatisticsTimeZoneAction(db, taskEnqueuer),
		listDeploymentAccounts:   deploymentaction.NewListAccountsQuery(db),
		updateDeploymentAccount:  deploymentaction.NewUpdateAccountAction(db),
		listDeploymentWorkspaces: deploymentaction.NewListWorkspacesQuery(db),
		setWorkspaceStatus:       deploymentaction.NewSetWorkspaceStatusAction(db),
		deploymentUsage:          deploymentaction.NewUsageQuery(db),
		deploymentTaskQueues:     deploymentaction.NewTaskQueuesQuery(db),
		deploymentFailedTasks:    deploymentaction.NewFailedTaskListQuery(db),
		instanceLicenseRead:      deploymentaction.NewLicenseQuery(db),
		activateInstanceLicense:  deploymentaction.NewActivateLicenseAction(db, licenseKeys),
		onlineInstanceLicense:    deploymentaction.NewOnlineLicenseAction(db, licenseKeys, controlClient),
		updateTelemetry:          deploymentaction.NewUpdateTelemetryAction(db),
	}
}

// GetDeploymentOverview 返回实例标识、规模、活跃趋势和实例能力。
func (o *directOperations) GetDeploymentOverview(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.DeploymentOverview, error) {
	overview, err := o.deploymentOverview.Execute(ctx)
	if err != nil {
		return appservice.DeploymentOverview{}, deploymentError(ctx, meta, err, i18n.ErrorDeploymentOverviewFailed, account, "")
	}
	trend := make([]appservice.DeploymentDailyActivity, 0, len(overview.Trend))
	for _, day := range overview.Trend {
		trend = append(trend, appservice.DeploymentDailyActivity{
			Date: day.Date.Format(time.DateOnly), ActiveAccounts: day.ActiveAccounts, ActiveWorkspaces: day.ActiveWorkspaces,
			NewAccounts: day.NewAccounts, NewWorkspaces: day.NewWorkspaces,
		})
	}
	return appservice.DeploymentOverview{
		InstanceID: overview.InstanceID, InstalledAt: overview.InstalledAt, StatisticsTimeZone: overview.StatisticsTimeZone,
		StatsRebuilding: overview.StatsRebuilding,
		AccountCount:    overview.AccountCount, WorkspaceCount: overview.WorkspaceCount, MemberCount: overview.MemberCount,
		Last7Days: appservice.DeploymentActivityWindow(overview.Last7Days), Last30Days: appservice.DeploymentActivityWindow(overview.Last30Days),
		Trend: trend,
		Capabilities: appservice.InstanceCapabilities{
			WorkspaceLimit: overview.Capabilities.WorkspaceLimit, CustomBranding: overview.Capabilities.CustomBranding,
		},
	}, nil
}

// GetDeploymentSettings 返回部署注册策略、工作区创建策略、统计时区和运行指标上报开关。
func (o *directOperations) GetDeploymentSettings(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.DeploymentSettings, error) {
	settings, err := o.deploymentSettingsRead.Execute(ctx)
	if err != nil {
		return appservice.DeploymentSettings{}, deploymentError(ctx, meta, err, i18n.ErrorDeploymentSettingsReadFailed, account, "")
	}
	return deploymentSettingsFromAction(settings), nil
}

// UpdateDeploymentSettings 修改部署注册策略和工作区创建策略。
func (o *directOperations) UpdateDeploymentSettings(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.DeploymentPoliciesInput) (appservice.DeploymentSettings, error) {
	settings, err := o.updateDeploymentPolicies.Execute(ctx, account, deploymentaction.Policies{
		RegistrationPolicy:      domain.RegistrationPolicy(input.RegistrationPolicy),
		WorkspaceCreationPolicy: domain.WorkspaceCreationPolicy(input.WorkspaceCreationPolicy),
	})
	if err != nil {
		return appservice.DeploymentSettings{}, deploymentError(ctx, meta, err, i18n.ErrorDeploymentSettingsUpdateFailed, account, "")
	}
	slog.Info("部署策略已修改", "account_id", account.Account.ID,
		"registration_policy", settings.RegistrationPolicy, "workspace_creation_policy", settings.WorkspaceCreationPolicy)
	return deploymentSettingsFromAction(settings), nil
}

// UpdateDeploymentStatisticsTimeZone 修改运营数据统计时区，并按新时区在后台重建运营数据。
func (o *directOperations) UpdateDeploymentStatisticsTimeZone(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.DeploymentStatisticsTimeZoneInput) (appservice.DeploymentSettings, error) {
	settings, err := o.updateStatisticsTimeZone.Execute(ctx, account, input.StatisticsTimeZone)
	if err != nil {
		return appservice.DeploymentSettings{}, deploymentError(ctx, meta, err, i18n.ErrorDeploymentSettingsUpdateFailed, account, "")
	}
	slog.Info("统计时区已修改", "account_id", account.Account.ID, "statistics_time_zone", settings.StatisticsTimeZone)
	return deploymentSettingsFromAction(settings), nil
}

// UpdateDeploymentTelemetry 开启或关闭向 control 上报运行指标。
func (o *directOperations) UpdateDeploymentTelemetry(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.DeploymentTelemetryInput) (appservice.DeploymentSettings, error) {
	settings, err := o.updateTelemetry.Execute(ctx, account, input.TelemetryEnabled)
	if err != nil {
		return appservice.DeploymentSettings{}, deploymentError(ctx, meta, err, i18n.ErrorDeploymentSettingsUpdateFailed, account, "")
	}
	slog.Info("运行指标上报开关已修改", "account_id", account.Account.ID, "telemetry_enabled", settings.TelemetryEnabled)
	return deploymentSettingsFromAction(settings), nil
}

// ListDeploymentAccounts 返回部署内的账号。
func (o *directOperations) ListDeploymentAccounts(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.DeploymentAccountListInput) (appservice.DeploymentAccountList, error) {
	output, err := o.listDeploymentAccounts.Execute(ctx, deploymentaction.AccountListInput{
		Query: input.Query, Status: domain.AccountStatus(input.Status), Page: input.Page, PageSize: input.PageSize,
	})
	if err != nil {
		return appservice.DeploymentAccountList{}, deploymentError(ctx, meta, err, i18n.ErrorDeploymentAccountListFailed, account, "")
	}
	accounts := make([]appservice.DeploymentAccount, 0, len(output.Accounts))
	for _, record := range output.Accounts {
		accounts = append(accounts, deploymentAccountFromAction(record))
	}
	return appservice.DeploymentAccountList{Accounts: accounts, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}

// DeactivateDeploymentAccount 停用其他账号并使其登录会话失效。
func (o *directOperations) DeactivateDeploymentAccount(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, accountID string) (appservice.DeploymentAccount, error) {
	record, err := o.updateDeploymentAccount.SetStatus(ctx, account, accountID, domain.AccountStatusInactive)
	return deploymentAccountResult(ctx, meta, account, accountID, record, err, "账号已停用")
}

// ReactivateDeploymentAccount 恢复已停用的其他账号。
func (o *directOperations) ReactivateDeploymentAccount(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, accountID string) (appservice.DeploymentAccount, error) {
	record, err := o.updateDeploymentAccount.SetStatus(ctx, account, accountID, domain.AccountStatusActive)
	return deploymentAccountResult(ctx, meta, account, accountID, record, err, "账号已恢复")
}

// GrantDeploymentAdmin 把其他账号设为部署管理员。
func (o *directOperations) GrantDeploymentAdmin(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, accountID string) (appservice.DeploymentAccount, error) {
	record, err := o.updateDeploymentAccount.SetDeploymentAdmin(ctx, account, accountID, true)
	return deploymentAccountResult(ctx, meta, account, accountID, record, err, "已设为部署管理员")
}

// RevokeDeploymentAdmin 撤销其他账号的部署管理员身份。
func (o *directOperations) RevokeDeploymentAdmin(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, accountID string) (appservice.DeploymentAccount, error) {
	record, err := o.updateDeploymentAccount.SetDeploymentAdmin(ctx, account, accountID, false)
	return deploymentAccountResult(ctx, meta, account, accountID, record, err, "已撤销部署管理员")
}

// ListDeploymentWorkspaces 返回部署内的全部工作区及其状态和当前规模。
func (o *directOperations) ListDeploymentWorkspaces(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.DeploymentWorkspaceListInput) (appservice.DeploymentWorkspaceList, error) {
	output, err := o.listDeploymentWorkspaces.Execute(ctx, deploymentaction.WorkspaceListInput{
		Query: input.Query, Status: domain.OrganizationLifecycleStatus(input.Status), Sort: deploymentaction.WorkspaceSort(input.Sort),
		Page: input.Page, PageSize: input.PageSize,
	})
	if err != nil {
		return appservice.DeploymentWorkspaceList{}, deploymentError(ctx, meta, err, i18n.ErrorWorkspaceListFailed, account, "")
	}
	workspaces := make([]appservice.DeploymentWorkspace, 0, len(output.Workspaces))
	for _, record := range output.Workspaces {
		workspaces = append(workspaces, deploymentWorkspaceFromAction(record))
	}
	return appservice.DeploymentWorkspaceList{Workspaces: workspaces, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}

// GetInstanceLicense 返回实例标识与实例授权状态。
func (o *directOperations) GetInstanceLicense(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.InstanceLicense, error) {
	current, err := o.instanceLicenseRead.Execute(ctx)
	if err != nil {
		return appservice.InstanceLicense{}, deploymentError(ctx, meta, err, i18n.ErrorInstanceLicenseReadFailed, account, "")
	}
	return instanceLicenseFromAction(current), nil
}

// ActivateInstanceLicense 用 control 签发的授权码离线激活或替换实例授权。
func (o *directOperations) ActivateInstanceLicense(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.ActivateInstanceLicenseInput) (appservice.InstanceLicense, error) {
	current, err := o.activateInstanceLicense.Execute(ctx, account, input.LicenseCode)
	if err != nil {
		return appservice.InstanceLicense{}, deploymentError(ctx, meta, err, i18n.ErrorInstanceLicenseActivateFailed, account, "")
	}
	slog.Info("实例授权已激活", "account_id", account.Account.ID, "license_id", current.LicenseID, "expires_at", current.ExpiresAt)
	return instanceLicenseFromAction(current), nil
}

// ActivateInstanceLicenseOnline 用激活码经 control 在线激活实例授权。
func (o *directOperations) ActivateInstanceLicenseOnline(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.ActivateInstanceLicenseOnlineInput) (appservice.InstanceLicense, error) {
	current, err := o.onlineInstanceLicense.Activate(ctx, account, input.ActivationCode)
	if err != nil {
		return appservice.InstanceLicense{}, deploymentError(ctx, meta, err, i18n.ErrorInstanceLicenseActivateFailed, account, "")
	}
	slog.Info("实例授权已在线激活", "account_id", account.Account.ID, "license_id", current.LicenseID, "expires_at", current.ExpiresAt)
	return instanceLicenseFromAction(current), nil
}

// SyncInstanceLicense 立即向 control 登记实例并拉取最新授权。
func (o *directOperations) SyncInstanceLicense(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.InstanceLicense, error) {
	current, err := o.onlineInstanceLicense.Sync(ctx, account)
	if errors.Is(err, deploymentaction.ErrLicenseExpired) {
		return appservice.InstanceLicense{}, appservice.InvalidError(meta, i18n.ErrorInstanceLicenseRenewalRequired, nil)
	}
	if err != nil {
		return appservice.InstanceLicense{}, deploymentError(ctx, meta, err, i18n.ErrorInstanceLicenseSyncFailed, account, "")
	}
	slog.Info("实例授权已同步", "account_id", account.Account.ID, "license_id", current.LicenseID, "expires_at", current.ExpiresAt)
	return instanceLicenseFromAction(current), nil
}

// instanceLicenseFromAction 把实例授权状态转换为应用契约，未激活时不返回授权字段。
func instanceLicenseFromAction(current deploymentaction.License) appservice.InstanceLicense {
	output := appservice.InstanceLicense{
		InstanceID: current.InstanceID, Status: appservice.LicenseStatus(current.Status),
		Capabilities: appservice.InstanceCapabilities{
			WorkspaceLimit: current.Capabilities.WorkspaceLimit, CustomBranding: current.Capabilities.CustomBranding,
		},
	}
	if current.Status != domain.LicenseStatusNone {
		output.LicenseID, output.Customer = current.LicenseID, current.Customer
		output.IssuedAt, output.ExpiresAt = &current.IssuedAt, &current.ExpiresAt
	}
	return output
}

// SuspendDeploymentWorkspace 暂停没有部署管理员成员的工作区：成员无法进入，渠道停止接待客户，后台任务挂起。
func (o *directOperations) SuspendDeploymentWorkspace(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, workspaceID string) (appservice.DeploymentWorkspace, error) {
	return o.setDeploymentWorkspaceStatus(ctx, meta, account, workspaceID, domain.OrganizationLifecycleSuspended, "工作区已暂停")
}

// ResumeDeploymentWorkspace 恢复已暂停的工作区并重新执行挂起的后台任务。
func (o *directOperations) ResumeDeploymentWorkspace(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, workspaceID string) (appservice.DeploymentWorkspace, error) {
	return o.setDeploymentWorkspaceStatus(ctx, meta, account, workspaceID, domain.OrganizationLifecycleActive, "工作区已恢复")
}

// setDeploymentWorkspaceStatus 切换工作区状态并转换结果，成功时记录日志。
func (o *directOperations) setDeploymentWorkspaceStatus(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, workspaceID string, status domain.OrganizationLifecycleStatus, message string) (appservice.DeploymentWorkspace, error) {
	record, err := o.setWorkspaceStatus.Execute(ctx, account, workspaceID, status)
	if errors.Is(err, deploymentaction.ErrWorkspaceNotFound) {
		return appservice.DeploymentWorkspace{}, appservice.NotFoundError(meta, i18n.ErrorDeploymentWorkspaceNotFound)
	}
	if errors.Is(err, deploymentaction.ErrWorkspaceHasDeploymentAdmin) {
		return appservice.DeploymentWorkspace{}, appservice.InvalidError(meta, i18n.ErrorDeploymentWorkspaceHasAdmin, nil)
	}
	if err != nil {
		return appservice.DeploymentWorkspace{}, deploymentError(ctx, meta, err, i18n.ErrorDeploymentWorkspaceUpdateFailed, account, "")
	}
	slog.Info(message, "operator_account_id", account.Account.ID, "workspace_id", workspaceID)
	return deploymentWorkspaceFromAction(record), nil
}

// GetDeploymentUsage 返回部署整体最近若干天的客服业务使用指标。
func (o *directOperations) GetDeploymentUsage(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.DeploymentUsageInput) (appservice.DeploymentUsageMetrics, error) {
	metrics, err := o.deploymentUsage.Summary(ctx, input.Days)
	if err != nil {
		return appservice.DeploymentUsageMetrics{}, deploymentError(ctx, meta, err, i18n.ErrorDeploymentUsageFailed, account, "")
	}
	return appservice.DeploymentUsageMetrics(metrics), nil
}

// ListDeploymentWorkspaceUsage 返回各工作区最近若干天的客服业务使用指标。
func (o *directOperations) ListDeploymentWorkspaceUsage(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.DeploymentWorkspaceUsageListInput) (appservice.DeploymentWorkspaceUsageList, error) {
	output, err := o.deploymentUsage.ListWorkspaces(ctx, deploymentaction.UsageListInput{
		Days: input.Days, Sort: deploymentaction.UsageSort(input.Sort), Page: input.Page, PageSize: input.PageSize,
	})
	if err != nil {
		return appservice.DeploymentWorkspaceUsageList{}, deploymentError(ctx, meta, err, i18n.ErrorDeploymentUsageFailed, account, "")
	}
	workspaces := make([]appservice.DeploymentWorkspaceUsage, 0, len(output.Workspaces))
	for _, record := range output.Workspaces {
		workspaces = append(workspaces, appservice.DeploymentWorkspaceUsage{
			ID: record.ID, Name: record.Name, Slug: record.Slug, Status: appservice.WorkspaceStatus(record.Status),
			Metrics: appservice.DeploymentUsageMetrics(record.UsageMetrics),
		})
	}
	return appservice.DeploymentWorkspaceUsageList{
		Workspaces: workspaces,
		Page:       appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total},
	}, nil
}

// GetDeploymentRuntimeStatus 返回服务端版本与后台任务各队列的运行概况。
func (o *directOperations) GetDeploymentRuntimeStatus(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.DeploymentRuntimeStatus, error) {
	queues, err := o.deploymentTaskQueues.Execute(ctx)
	if err != nil {
		return appservice.DeploymentRuntimeStatus{}, deploymentError(ctx, meta, err, i18n.ErrorDeploymentRuntimeFailed, account, "")
	}
	status := appservice.DeploymentRuntimeStatus{Version: buildinfo.Version, Queues: make([]appservice.DeploymentTaskQueue, 0, len(queues))}
	for _, queue := range queues {
		status.Queues = append(status.Queues, appservice.DeploymentTaskQueue(queue))
	}
	return status, nil
}

// ListDeploymentFailedTasks 返回等待重试与近 7 天内失败的后台任务。
func (o *directOperations) ListDeploymentFailedTasks(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.DeploymentFailedTaskListInput) (appservice.DeploymentFailedTaskList, error) {
	output, err := o.deploymentFailedTasks.Execute(ctx, deploymentaction.FailedTaskListInput{Page: input.Page, PageSize: input.PageSize})
	if err != nil {
		return appservice.DeploymentFailedTaskList{}, deploymentError(ctx, meta, err, i18n.ErrorDeploymentRuntimeFailed, account, "")
	}
	tasks := make([]appservice.DeploymentFailedTask, 0, len(output.Tasks))
	for _, task := range output.Tasks {
		tasks = append(tasks, appservice.DeploymentFailedTask{
			ID: task.ID, Action: task.ActionName, Queue: task.QueueName, WorkspaceName: task.WorkspaceName, Retrying: task.Retrying,
			Attempt: task.Attempt, MaxAttempts: task.MaxAttempts, Error: task.LastError, FailedAt: task.FailedAt,
		})
	}
	return appservice.DeploymentFailedTaskList{
		Tasks: tasks, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total},
	}, nil
}

// deploymentWorkspaceFromAction 把部署工作区记录转换为应用契约。
func deploymentWorkspaceFromAction(record deploymentaction.WorkspaceRecord) appservice.DeploymentWorkspace {
	var lastActiveOn *string
	if record.LastActiveOn != nil {
		value := record.LastActiveOn.Format(time.DateOnly)
		lastActiveOn = &value
	}
	return appservice.DeploymentWorkspace{
		ID: record.ID, Name: record.Name, Slug: record.Slug, Status: appservice.WorkspaceStatus(record.Status),
		MemberCount: record.MemberCount, AIEmployeeCount: record.AIEmployeeCount, ChannelCount: record.ChannelCount,
		DeviceCount: record.DeviceCount, HasDeploymentAdmin: record.HasAdmin, StorageBytes: record.StorageBytes, LastActiveOn: lastActiveOn, CreatedAt: record.CreatedAt,
	}
}

// deploymentSettingsFromAction 把部署级策略转换为应用契约。
func deploymentSettingsFromAction(settings deploymentaction.Settings) appservice.DeploymentSettings {
	return appservice.DeploymentSettings{
		RegistrationPolicy:      appservice.RegistrationPolicy(settings.RegistrationPolicy),
		WorkspaceCreationPolicy: appservice.WorkspaceCreationPolicy(settings.WorkspaceCreationPolicy),
		StatisticsTimeZone:      settings.StatisticsTimeZone,
		TelemetryEnabled:        settings.TelemetryEnabled,
	}
}

// deploymentAccountFromAction 把部署账号记录转换为应用契约。
func deploymentAccountFromAction(record deploymentaction.AccountRecord) appservice.DeploymentAccount {
	return appservice.DeploymentAccount{
		ID: record.ID, Email: record.Email, DisplayName: record.DisplayName, Status: appservice.AccountStatus(record.Status),
		IsDeploymentAdmin: record.IsDeploymentAdmin, WorkspaceCount: record.WorkspaceCount, CreatedAt: record.CreatedAt,
	}
}

// deploymentAccountResult 把部署账号修改结果转换为应用契约，成功时记录日志。
func deploymentAccountResult(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, accountID string, record deploymentaction.AccountRecord, err error, message string) (appservice.DeploymentAccount, error) {
	if err != nil {
		return appservice.DeploymentAccount{}, deploymentError(ctx, meta, err, i18n.ErrorDeploymentAccountUpdateFailed, account, accountID)
	}
	slog.Info(message, "operator_account_id", account.Account.ID, "account_id", accountID)
	return deploymentAccountFromAction(record), nil
}

// deploymentError 把部署管理操作的错误转换为本地化业务错误。
func deploymentError(ctx context.Context, meta appservice.RequestMeta, err error, failureKey i18n.Key, account *servermodels.AccountIdentity, accountID string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		// 把部署管理校验错误码映射为本地化文案键。
		keys := map[common.FieldCode]i18n.Key{
			deploymentaction.ValidationQueryInvalid:                   i18n.FieldDeploymentQueryInvalid,
			deploymentaction.ValidationAccountStatusInvalid:           i18n.FieldUserStatusInvalid,
			deploymentaction.ValidationRegistrationPolicyInvalid:      i18n.FieldRegistrationPolicyInvalid,
			deploymentaction.ValidationWorkspaceCreationPolicyInvalid: i18n.FieldWorkspaceCreationPolicyInvalid,
			deploymentaction.ValidationStatisticsTimeZoneInvalid:      i18n.FieldTimeZoneInvalid,
			deploymentaction.ValidationWorkspaceSortInvalid:           i18n.FieldDeploymentQueryInvalid,
			deploymentaction.ValidationWorkspaceStatusInvalid:         i18n.FieldDeploymentQueryInvalid,
			deploymentaction.ValidationUsageSortInvalid:               i18n.FieldDeploymentQueryInvalid,
			deploymentaction.ValidationUsageDaysInvalid:               i18n.FieldDeploymentQueryInvalid,
		}
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, keys))
	}
	if errors.Is(err, deploymentaction.ErrNotDeploymentAdmin) {
		return appservice.ForbiddenError(meta, i18n.ErrorDeploymentAdminRequired)
	}
	if errors.Is(err, deploymentaction.ErrAccountNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorDeploymentAccountNotFound)
	}
	if errors.Is(err, deploymentaction.ErrSelfChange) {
		return appservice.InvalidError(meta, i18n.ErrorDeploymentAccountSelfChange, nil)
	}
	if errors.Is(err, deploymentaction.ErrNoActiveMembership) {
		return appservice.InvalidError(meta, i18n.ErrorDeploymentAccountNoWorkspace, nil)
	}
	// 把授权码校验与 control 通信错误映射为本地化文案键。
	licenseErrors := map[error]i18n.Key{
		deploymentaction.ErrLicenseInvalid:             i18n.ErrorInstanceLicenseInvalid,
		deploymentaction.ErrLicenseInstanceMismatch:    i18n.ErrorInstanceLicenseInstanceMismatch,
		deploymentaction.ErrLicenseExpired:             i18n.ErrorInstanceLicenseExpired,
		deploymentaction.ErrLicenseSuperseded:          i18n.ErrorInstanceLicenseSuperseded,
		deploymentaction.ErrActivationCodeInvalid:      i18n.ErrorActivationCodeInvalid,
		deploymentaction.ErrActivationInstanceMismatch: i18n.ErrorActivationInstanceMismatch,
		deploymentaction.ErrInstanceKeyMismatch:        i18n.ErrorInstanceKeyMismatch,
		deploymentaction.ErrLicenseNotIssued:           i18n.ErrorInstanceLicenseNotIssued,
		deploymentaction.ErrControlUnavailable:         i18n.ErrorControlUnavailable,
	}
	for target, key := range licenseErrors {
		if errors.Is(err, target) {
			return appservice.InvalidError(meta, key, nil)
		}
	}
	attributes := []any{"account_id", account.Account.ID, "failure", failureKey, "error", err}
	if accountID != "" {
		attributes = append(attributes, "target_account_id", accountID)
	}
	slog.Warn("部署管理操作失败", attributes...)
	return appservice.FailedError(meta, failureKey)
}

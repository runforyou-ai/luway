//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/common/buildinfo"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// deploymentOps 持有部署管理的 Action 和 Query。
type deploymentOps struct {
	deploymentOverview       *deploymentaction.OverviewQuery
	deploymentSettingsRead   *deploymentaction.SettingsQuery
	updateDeploymentSettings *deploymentaction.UpdateSettingsAction
	listDeploymentAccounts   *deploymentaction.ListAccountsQuery
	updateDeploymentAccount  *deploymentaction.UpdateAccountAction
	listDeploymentWorkspaces *deploymentaction.ListWorkspacesQuery
}

// newDeploymentOps 创建部署管理的业务实现依赖。
func newDeploymentOps(db *bun.DB) deploymentOps {
	return deploymentOps{
		deploymentOverview:       deploymentaction.NewOverviewQuery(db),
		deploymentSettingsRead:   deploymentaction.NewSettingsQuery(db),
		updateDeploymentSettings: deploymentaction.NewUpdateSettingsAction(db),
		listDeploymentAccounts:   deploymentaction.NewListAccountsQuery(db),
		updateDeploymentAccount:  deploymentaction.NewUpdateAccountAction(db),
		listDeploymentWorkspaces: deploymentaction.NewListWorkspacesQuery(db),
	}
}

// GetDeploymentOverview 返回实例标识、服务端版本、账号与工作区数量和实例能力。
func (o *directOperations) GetDeploymentOverview(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.DeploymentOverview, error) {
	overview, err := o.deploymentOverview.Execute(ctx)
	if err != nil {
		return appservice.DeploymentOverview{}, deploymentError(ctx, meta, err, i18n.ErrorDeploymentOverviewFailed, account, "")
	}
	return appservice.DeploymentOverview{
		InstanceID: overview.InstanceID, Version: buildinfo.Version, InstalledAt: overview.InstalledAt,
		AccountCount: overview.AccountCount, WorkspaceCount: overview.WorkspaceCount,
		Capabilities: appservice.InstanceCapabilities{
			WorkspaceLimit: overview.Capabilities.WorkspaceLimit, CustomBranding: overview.Capabilities.CustomBranding,
		},
	}, nil
}

// GetDeploymentSettings 返回部署注册策略和工作区创建策略。
func (o *directOperations) GetDeploymentSettings(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.DeploymentSettings, error) {
	settings, err := o.deploymentSettingsRead.Execute(ctx)
	if err != nil {
		return appservice.DeploymentSettings{}, deploymentError(ctx, meta, err, i18n.ErrorDeploymentSettingsReadFailed, account, "")
	}
	return deploymentSettingsFromAction(settings), nil
}

// UpdateDeploymentSettings 修改部署注册策略和工作区创建策略。
func (o *directOperations) UpdateDeploymentSettings(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.DeploymentSettings) (appservice.DeploymentSettings, error) {
	settings, err := o.updateDeploymentSettings.Execute(ctx, account, deploymentaction.Settings{
		RegistrationPolicy:      domain.RegistrationPolicy(input.RegistrationPolicy),
		WorkspaceCreationPolicy: domain.WorkspaceCreationPolicy(input.WorkspaceCreationPolicy),
	})
	if err != nil {
		return appservice.DeploymentSettings{}, deploymentError(ctx, meta, err, i18n.ErrorDeploymentSettingsUpdateFailed, account, "")
	}
	slog.Info("部署设置已修改", "account_id", account.Account.ID,
		"registration_policy", settings.RegistrationPolicy, "workspace_creation_policy", settings.WorkspaceCreationPolicy)
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

// ListDeploymentWorkspaces 返回部署内的全部工作区。
func (o *directOperations) ListDeploymentWorkspaces(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.DeploymentWorkspaceListInput) (appservice.DeploymentWorkspaceList, error) {
	output, err := o.listDeploymentWorkspaces.Execute(ctx, deploymentaction.WorkspaceListInput{Query: input.Query, Page: input.Page, PageSize: input.PageSize})
	if err != nil {
		return appservice.DeploymentWorkspaceList{}, deploymentError(ctx, meta, err, i18n.ErrorWorkspaceListFailed, account, "")
	}
	workspaces := make([]appservice.DeploymentWorkspace, 0, len(output.Workspaces))
	for _, record := range output.Workspaces {
		workspaces = append(workspaces, appservice.DeploymentWorkspace{
			ID: record.ID, Name: record.Name, Slug: record.Slug, MemberCount: record.MemberCount, CreatedAt: record.CreatedAt,
		})
	}
	return appservice.DeploymentWorkspaceList{Workspaces: workspaces, Page: appservice.PageInfo{Number: output.Page.Number, Size: output.Page.Size, Total: output.Page.Total}}, nil
}

// deploymentSettingsFromAction 把部署级策略转换为应用契约。
func deploymentSettingsFromAction(settings deploymentaction.Settings) appservice.DeploymentSettings {
	return appservice.DeploymentSettings{
		RegistrationPolicy:      appservice.RegistrationPolicy(settings.RegistrationPolicy),
		WorkspaceCreationPolicy: appservice.WorkspaceCreationPolicy(settings.WorkspaceCreationPolicy),
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
	attributes := []any{"account_id", account.Account.ID, "failure", failureKey, "error", err}
	if accountID != "" {
		attributes = append(attributes, "target_account_id", accountID)
	}
	slog.Warn("部署管理操作失败", attributes...)
	return appservice.FailedError(meta, failureKey)
}

//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// UpdateOrganization 修改当前工作区的名称。
func (o *directOperations) UpdateOrganization(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.OrganizationInput) (appservice.Organization, error) {
	organization, err := o.updateOrganization.Execute(ctx, identity, input.Name)
	if err != nil {
		return appservice.Organization{}, o.organizationMutationError(ctx, meta, err, i18n.ErrorOrganizationUpdateFailed, identity.Organization.ID)
	}
	slog.Info("工作区通用设置更新成功", "organization_id", organization.ID)
	return organizationFromModel(*organization), nil
}

// organizationMutationError 转换工作区设置写入错误。
func (o *directOperations) organizationMutationError(ctx context.Context, meta appservice.RequestMeta, err error, failureKey i18n.Key, organizationID string) error {
	if mapped := commonActionError(ctx, meta, err); mapped != nil {
		return mapped
	}
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, workspaceFieldKeys(validationError.Fields))
	}
	slog.Warn("工作区设置操作失败", "organization_id", organizationID, "failure", failureKey, "error", err)
	return appservice.FailedError(meta, failureKey)
}

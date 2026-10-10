//go:build server

package direct

import (
	"context"
	"log/slog"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// UpdateWorkspace 修改当前工作区的名称。
func (o *directoryOps) UpdateWorkspace(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.WorkspaceSettingsInput) (appservice.CurrentWorkspace, error) {
	workspace, err := o.updateWorkspace.Execute(ctx, identity, input.Name)
	if err != nil {
		return appservice.CurrentWorkspace{}, workspaceMutationError(meta, err, i18n.ErrorWorkspaceUpdateFailed)
	}
	slog.InfoContext(logscope.WithWorkspace(ctx, workspace.ID), "工作区通用设置更新成功")
	return workspaceFromModel(*workspace), nil
}

// workspaceMutationErrors 是工作区设置写入的错误转换规则。
var workspaceMutationErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{dispatch.FieldRule(workspaceFieldKeys)})

// workspaceMutationError 转换工作区设置写入错误。
func workspaceMutationError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return workspaceMutationErrors.Translate(meta, err, failureKey)
}

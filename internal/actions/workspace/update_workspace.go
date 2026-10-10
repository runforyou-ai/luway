//go:build server

// Package workspace 实现工作区创建、通用设置修改与账号工作区查询。
package workspace

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/common"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	"github.com/uptrace/bun"
)

// ValidationCode 标识工作区设置的校验结果。
type ValidationCode = common.FieldCode

const (
	// ValidationNameRequired 表示工作区名称为空。
	ValidationNameRequired ValidationCode = "WORKSPACE_NAME_REQUIRED"
	// ValidationNameTooLong 表示工作区名称超过长度上限。
	ValidationNameTooLong ValidationCode = "WORKSPACE_NAME_TOO_LONG"
	// ValidationNameDuplicate 表示平台中已有同名工作区。
	ValidationNameDuplicate ValidationCode = "WORKSPACE_NAME_DUPLICATE"
)

// ValidationError 表示工作区设置校验失败。
type ValidationError = common.FieldError

// UpdateWorkspaceAction 修改工作区通用设置。
type UpdateWorkspaceAction struct {
	db *bun.DB
}

// NewUpdateWorkspaceAction 创建工作区通用设置修改操作。
func NewUpdateWorkspaceAction(db *bun.DB) *UpdateWorkspaceAction {
	return &UpdateWorkspaceAction{db: db}
}

// Execute 校验并修改当前成员所在工作区的名称，访问标识保持固定。
func (a *UpdateWorkspaceAction) Execute(ctx context.Context, identity *servermodels.Identity, name string) (*servermodels.Workspace, error) {
	name, fields := normalizeWorkspaceName(name)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	var workspace *servermodels.Workspace
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		workspace = &servermodels.Workspace{ID: identity.Workspace.ID, Name: name}
		_, err := tx.NewUpdate().
			Model(workspace).
			Column("name").
			WherePK().
			Returning("*").
			Exec(ctx)
		return err
	})
	if pgerr.UniqueViolationOn(err, "workspaces_name_unique") {
		return nil, &ValidationError{Fields: map[string]ValidationCode{"name": ValidationNameDuplicate}}
	}
	if err != nil {
		return nil, fmt.Errorf("update workspace: %w", err)
	}
	return workspace, nil
}

//go:build server

package role

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// CreateRoleAction 创建自定义角色。
type CreateRoleAction struct {
	db *bun.DB
}

// NewCreateRoleAction 创建角色操作。
func NewCreateRoleAction(db *bun.DB) *CreateRoleAction {
	return &CreateRoleAction{db: db}
}

// Execute 创建自定义角色并保存权限。
func (a *CreateRoleAction) Execute(ctx context.Context, identity *servermodels.Identity, input Input) (*Record, error) {
	input, fields := normalizeInput(input, true)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	var role servermodels.Role
	err := serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		workspace := &servermodels.Workspace{}
		if err := tx.NewSelect().Model(workspace).
			Where("id = ?", identity.Workspace.ID).
			For("UPDATE").
			Scan(ctx); err != nil {
			return err
		}
		count, err := tx.NewSelect().Model((*servermodels.Role)(nil)).
			Where("workspace_id = ?", identity.Workspace.ID).
			Count(ctx)
		if err != nil {
			return err
		}
		if count >= MaxRolesPerWorkspace {
			return ErrLimitReached
		}
		role = servermodels.Role{
			WorkspaceID: identity.Workspace.ID,
			Kind:        string(domain.RoleKindCustom),
			Name:        input.Name,
			Description: input.Description,
		}
		if _, err := tx.NewInsert().
			Model(&role).
			Column("workspace_id", "kind", "name", "description").
			Returning("*").
			Exec(ctx); err != nil {
			return err
		}
		return replacePermissions(ctx, tx, identity.Workspace.ID, role.ID, input.Permissions)
	})
	if isRoleNameConflict(err) {
		return nil, &ValidationError{Fields: map[string]ValidationCode{"name": ValidationNameDuplicate}}
	}
	if err != nil {
		return nil, fmt.Errorf("create role: %w", err)
	}
	return new(recordFromModel(role, input.Permissions, 0)), nil
}

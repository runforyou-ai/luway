//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	roleaction "github.com/runforyou-ai/luway/internal/actions/role"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
)

// ListRoleOptions 返回成员表单与筛选使用的角色选项。
func (o *directoryOps) ListRoleOptions(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.RoleOptionList, error) {
	options, err := o.listRoleOptions.Execute(ctx, identity)
	if err != nil {
		return appservice.RoleOptionList{}, roleError(meta, err, i18n.ErrorRoleListFailed)
	}
	return appservice.RoleOptionList{Roles: arr.Map(options, func(option roleaction.Option) appservice.RoleOption {
		return appservice.RoleOption{ID: option.ID, Kind: option.Kind, Name: option.Name, Assignable: option.Assignable}
	})}, nil
}

// ListRoles 返回当前企业的角色和预定义权限目录。
func (o *directoryOps) ListRoles(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.RoleList, error) {
	output, err := o.listRoles.Execute(ctx, identity)
	if err != nil {
		return appservice.RoleList{}, roleError(meta, err, i18n.ErrorRoleListFailed)
	}
	roles := arr.Map(output.Roles, roleFromAction)
	permissions := arr.Map(output.Permissions, func(permission domain.PermissionDefinition) appservice.PermissionCode {
		return appservice.PermissionCode(permission.Code)
	})
	return appservice.RoleList{Roles: roles, Permissions: permissions, Maximum: roleaction.MaxRolesPerWorkspace}, nil
}

// GetRole 返回当前企业的角色详情。
func (o *directoryOps) GetRole(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, roleID string) (appservice.Role, error) {
	role, err := o.getRole.Execute(ctx, identity, roleID)
	if err != nil {
		return appservice.Role{}, roleError(meta, err, i18n.ErrorRoleReadFailed)
	}
	return roleFromAction(*role), nil
}

// CreateRole 创建自定义角色。
func (o *directoryOps) CreateRole(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.RoleInput) (appservice.Role, error) {
	role, err := o.createRole.Execute(ctx, identity, roleInput(input))
	if err != nil {
		return appservice.Role{}, roleMutationError(meta, err, i18n.ErrorRoleCreateFailed)
	}
	slog.InfoContext(ctx, "角色创建成功", "role_id", role.ID, "permission_count", len(role.Permissions))
	return roleFromAction(*role), nil
}

// UpdateRole 修改角色信息和权限。
func (o *directoryOps) UpdateRole(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, roleID string, input appservice.RoleInput) (appservice.Role, error) {
	role, err := o.updateRole.Execute(ctx, identity, roleID, roleInput(input))
	if err != nil {
		return appservice.Role{}, roleMutationError(meta, err, i18n.ErrorRoleUpdateFailed)
	}
	slog.InfoContext(ctx, "角色保存成功", "role_id", role.ID, "role_kind", role.Kind, "permission_count", len(role.Permissions))
	return roleFromAction(*role), nil
}

// DeleteRole 删除自定义角色。
func (o *directoryOps) DeleteRole(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, roleID string) error {
	if err := o.deleteRole.Execute(ctx, identity, roleID); err != nil {
		if errors.Is(err, roleaction.ErrBuiltInDeleteForbidden) {
			return appservice.InvalidError(meta, i18n.ErrorRoleBuiltInDeleteForbidden, nil)
		}
		return roleError(meta, err, i18n.ErrorRoleDeleteFailed)
	}
	slog.InfoContext(ctx, "角色删除成功", "role_id", roleID)
	return nil
}

// UpdateRoleAssignments 在一个事务中批量调整成员角色。
func (o *directoryOps) UpdateRoleAssignments(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.RoleAssignmentsInput) error {
	assignments := arr.Map(input.Assignments, func(assignment appservice.RoleAssignmentInput) roleaction.AssignmentInput {
		return roleaction.AssignmentInput{IdentityID: assignment.IdentityID, RoleID: assignment.RoleID}
	})
	if err := o.updateRoleAssignments.Execute(ctx, identity, assignments); err != nil {
		if errors.Is(err, roleaction.ErrAssignmentInvalid) {
			return appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
		}
		if errors.Is(err, roleaction.ErrLastActiveAdministrator) {
			return appservice.InvalidError(meta, i18n.ErrorUserLastActiveAdministrator, nil)
		}
		return roleError(meta, err, i18n.ErrorRoleUpdateFailed)
	}
	slog.InfoContext(ctx, "成员角色批量调整成功", "assignment_count", len(assignments))
	return nil
}

// roleFieldKeys 把角色校验错误码映射为本地化文案键。
var roleFieldKeys = map[common.FieldCode]i18n.Key{
	roleaction.ValidationNameRequired:       i18n.FieldRoleNameRequired,
	roleaction.ValidationNameTooLong:        i18n.FieldRoleNameTooLong,
	roleaction.ValidationNameDuplicate:      i18n.FieldRoleNameDuplicate,
	roleaction.ValidationDescriptionTooLong: i18n.FieldRoleDescriptionTooLong,
	roleaction.ValidationPermissionsInvalid: i18n.FieldRolePermissionsInvalid,
}

// roleMutationError 转换角色写入校验和操作错误。
func roleMutationError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return roleMutationErrors.Translate(meta, err, failureKey)
}

// roleErrors 是角色通用操作的错误转换规则。
var roleErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.Is(roleaction.ErrNotFound, dispatch.NotFound(i18n.ErrorRoleNotFound)),
	dispatch.Is(roleaction.ErrInUse, dispatch.Invalid(i18n.ErrorRoleInUse)),
})

// roleMutationErrors 是角色写入的错误转换规则。
var roleMutationErrors = dispatch.Catalogs(dispatch.Catalog{
	dispatch.FieldRule(roleFieldKeys),
	dispatch.Is(roleaction.ErrAdminImmutable, dispatch.Invalid(i18n.ErrorRoleAdminImmutable)),
	dispatch.Is(roleaction.ErrLimitReached, dispatch.Invalid(i18n.ErrorRoleLimitReached)),
}, roleErrors)

// roleError 转换角色通用操作错误。
func roleError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	return roleErrors.Translate(meta, err, failureKey)
}

// roleInput 转换角色输入。
func roleInput(input appservice.RoleInput) roleaction.Input {
	permissions := append(make([]domain.PermissionCode, 0, len(input.Permissions)), input.Permissions...)
	return roleaction.Input{Name: input.Name, Description: input.Description, Permissions: permissions}
}

// roleFromAction 转换角色输出。
func roleFromAction(input roleaction.Record) appservice.Role {
	permissions := append(make([]appservice.PermissionCode, 0, len(input.Permissions)), input.Permissions...)
	return appservice.Role{
		ID: input.ID, Kind: input.Kind, Name: input.Name, Description: input.Description,
		Permissions: permissions, MemberCount: input.MemberCount,
		CreatedAt: input.CreatedAt, UpdatedAt: input.UpdatedAt,
	}
}

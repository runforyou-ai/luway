//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	roleaction "github.com/runforyou-ai/luway/internal/actions/role"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// ListRoles 返回当前企业的角色和预定义权限目录。
func (o *directOperations) ListRoles(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity) (appservice.RoleList, error) {
	output, err := o.listRoles.Execute(ctx, identity)
	if err != nil {
		return appservice.RoleList{}, o.roleError(meta, err, i18n.ErrorRoleListFailed)
	}
	roles := make([]appservice.Role, 0, len(output.Roles))
	for _, role := range output.Roles {
		roles = append(roles, roleFromAction(role))
	}
	permissions := make([]appservice.PermissionDefinition, 0, len(output.Permissions))
	for _, permission := range output.Permissions {
		permissions = append(permissions, appservice.PermissionDefinition{
			Code: appservice.PermissionCode(permission.Code), Resource: appservice.PermissionResource(permission.Resource),
			Level: appservice.PermissionLevel(permission.Level),
		})
	}
	return appservice.RoleList{Roles: roles, Permissions: permissions, Maximum: roleaction.MaxRolesPerOrganization}, nil
}

// GetRole 返回当前企业的角色详情。
func (o *directOperations) GetRole(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, roleID string) (appservice.Role, error) {
	role, err := o.getRole.Execute(ctx, identity, roleID)
	if err != nil {
		return appservice.Role{}, o.roleError(meta, err, i18n.ErrorRoleReadFailed)
	}
	return roleFromAction(*role), nil
}

// CreateRole 创建自定义角色。
func (o *directOperations) CreateRole(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.RoleInput) (appservice.Role, error) {
	role, err := o.createRole.Execute(ctx, identity, roleInput(input))
	if err != nil {
		return appservice.Role{}, o.roleMutationError(meta, err, i18n.ErrorRoleCreateFailed)
	}
	slog.Info("角色创建成功", "organization_id", identity.Organization.ID, "role_id", role.ID, "permission_count", len(role.Permissions))
	return roleFromAction(*role), nil
}

// UpdateRole 修改角色信息和权限。
func (o *directOperations) UpdateRole(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, roleID string, input appservice.RoleInput) (appservice.Role, error) {
	role, err := o.updateRole.Execute(ctx, identity, roleID, roleInput(input))
	if err != nil {
		return appservice.Role{}, o.roleMutationError(meta, err, i18n.ErrorRoleUpdateFailed)
	}
	slog.Info("角色保存成功", "organization_id", identity.Organization.ID, "role_id", role.ID, "role_kind", role.Kind, "permission_count", len(role.Permissions))
	return roleFromAction(*role), nil
}

// DeleteRole 删除自定义角色。
func (o *directOperations) DeleteRole(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, roleID string) error {
	if err := o.deleteRole.Execute(ctx, identity, roleID); err != nil {
		if errors.Is(err, roleaction.ErrBuiltInDeleteForbidden) {
			return appservice.InvalidError(meta, i18n.ErrorRoleBuiltInDeleteForbidden, nil)
		}
		return o.roleError(meta, err, i18n.ErrorRoleDeleteFailed)
	}
	slog.Info("角色删除成功", "organization_id", identity.Organization.ID, "role_id", roleID)
	return nil
}

// UpdateRoleAssignments 在一个事务中批量调整成员角色。
func (o *directOperations) UpdateRoleAssignments(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, input appservice.RoleAssignmentsInput) error {
	assignments := make([]roleaction.AssignmentInput, 0, len(input.Assignments))
	for _, assignment := range input.Assignments {
		assignments = append(assignments, roleaction.AssignmentInput{IdentityID: assignment.IdentityID, RoleID: assignment.RoleID})
	}
	if err := o.updateRoleAssignments.Execute(ctx, identity, assignments); err != nil {
		if errors.Is(err, roleaction.ErrAssignmentInvalid) {
			return appservice.InvalidError(meta, i18n.ErrorValidationFailed, nil)
		}
		if errors.Is(err, roleaction.ErrLastActiveAdministrator) {
			return appservice.InvalidError(meta, i18n.ErrorUserLastActiveAdministrator, nil)
		}
		return o.roleError(meta, err, i18n.ErrorRoleUpdateFailed)
	}
	slog.Info("成员角色批量调整成功", "organization_id", identity.Organization.ID, "assignment_count", len(assignments))
	return nil
}

// roleMutationError 转换角色写入校验和操作错误。
func (o *directOperations) roleMutationError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		// 把角色校验错误码映射为本地化文案键。
		keys := map[common.FieldCode]i18n.Key{
			roleaction.ValidationNameRequired: i18n.FieldRoleNameRequired, roleaction.ValidationNameTooLong: i18n.FieldRoleNameTooLong,
			roleaction.ValidationNameDuplicate: i18n.FieldRoleNameDuplicate, roleaction.ValidationDescriptionTooLong: i18n.FieldRoleDescriptionTooLong,
			roleaction.ValidationPermissionsInvalid: i18n.FieldRolePermissionsInvalid,
		}
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, keys))
	}
	if errors.Is(err, roleaction.ErrAdminImmutable) {
		return appservice.InvalidError(meta, i18n.ErrorRoleAdminImmutable, nil)
	}
	if errors.Is(err, roleaction.ErrLimitReached) {
		return appservice.InvalidError(meta, i18n.ErrorRoleLimitReached, nil)
	}
	return o.roleError(meta, err, failureKey)
}

// roleError 转换角色通用操作错误。
func (o *directOperations) roleError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	if mapped := commonActionError(meta, err); mapped != nil {
		return mapped
	}
	if errors.Is(err, roleaction.ErrNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorRoleNotFound)
	}
	if errors.Is(err, roleaction.ErrInUse) {
		return appservice.InvalidError(meta, i18n.ErrorRoleInUse, nil)
	}
	return appservice.FailedError(meta, failureKey, err)
}

// roleInput 转换角色输入。
func roleInput(input appservice.RoleInput) roleaction.Input {
	permissions := make([]domain.PermissionCode, 0, len(input.Permissions))
	for _, permission := range input.Permissions {
		permissions = append(permissions, domain.PermissionCode(permission))
	}
	return roleaction.Input{Name: input.Name, Description: input.Description, Permissions: permissions}
}

// roleFromAction 转换角色输出。
func roleFromAction(input roleaction.Record) appservice.Role {
	permissions := make([]appservice.PermissionCode, 0, len(input.Permissions))
	for _, permission := range input.Permissions {
		permissions = append(permissions, appservice.PermissionCode(permission))
	}
	return appservice.Role{
		ID: input.ID, Kind: appservice.RoleKind(input.Kind), Name: input.Name, Description: input.Description,
		Permissions: permissions, MemberCount: input.MemberCount,
		CreatedAt: input.CreatedAt, UpdatedAt: input.UpdatedAt,
	}
}

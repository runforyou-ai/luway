//go:build server

package role

import (
	"strings"
	"unicode/utf8"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
)

// ValidationCode 标识角色字段校验结果。
type ValidationCode = common.FieldCode

const (
	ValidationNameRequired       ValidationCode = "ROLE_NAME_REQUIRED"
	ValidationNameTooLong        ValidationCode = "ROLE_NAME_TOO_LONG"
	ValidationNameDuplicate      ValidationCode = "ROLE_NAME_DUPLICATE"
	ValidationDescriptionTooLong ValidationCode = "ROLE_DESCRIPTION_TOO_LONG"
	ValidationPermissionsInvalid ValidationCode = "ROLE_PERMISSIONS_INVALID"
)

const (
	// maxRoleNameLength 是角色名称的最大字符数。
	maxRoleNameLength = 10
	// maxRoleDescriptionLength 是角色描述的最大字符数。
	maxRoleDescriptionLength = 200
)

// ValidationError 表示角色字段校验失败。
type ValidationError = common.FieldError

// normalizeInput 规范化可编辑字段并补齐管理权限依赖的查看权限。
func normalizeInput(input Input, custom bool) (Input, map[string]ValidationCode) {
	fields := make(map[string]ValidationCode)
	if custom {
		input.Name = strings.TrimSpace(input.Name)
		input.Description = strings.TrimSpace(input.Description)
		if input.Name == "" {
			fields["name"] = ValidationNameRequired
		} else if utf8.RuneCountInString(input.Name) > maxRoleNameLength {
			fields["name"] = ValidationNameTooLong
		}
		if utf8.RuneCountInString(input.Description) > maxRoleDescriptionLength {
			fields["description"] = ValidationDescriptionTooLong
		}
	} else {
		input.Name = ""
		input.Description = ""
	}

	selected := make(map[domain.PermissionCode]struct{}, len(input.Permissions))
	for _, permission := range input.Permissions {
		if !domain.IsPermissionCode(permission) {
			fields["permissions"] = ValidationPermissionsInvalid
			continue
		}
		selected[permission] = struct{}{}
		if dependency, ok := domain.PermissionViewDependency(permission); ok {
			selected[dependency] = struct{}{}
		}
	}

	input.Permissions = make([]domain.PermissionCode, 0, len(selected))
	for _, definition := range domain.PermissionDefinitions() {
		if _, ok := selected[definition.Code]; ok {
			input.Permissions = append(input.Permissions, definition.Code)
		}
	}
	return input, fields
}

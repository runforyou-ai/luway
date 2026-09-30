//go:build server

package role

import (
	"testing"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// TestNormalizeInputAddsViewDependency 验证管理权限自动补齐同功能的查看权限。
func TestNormalizeInputAddsViewDependency(t *testing.T) {
	input, fields := normalizeInput(Input{
		Name:        " 客服主管 ",
		Permissions: []domain.PermissionCode{domain.PermissionExternalContactsManage},
	}, true)
	if len(fields) != 0 {
		t.Fatalf("validation fields = %v, want empty", fields)
	}
	if input.Name != "客服主管" {
		t.Fatalf("normalized name = %q", input.Name)
	}
	if len(input.Permissions) != 2 ||
		input.Permissions[0] != domain.PermissionExternalContactsView ||
		input.Permissions[1] != domain.PermissionExternalContactsManage {
		t.Fatalf("permissions = %v, want view and manage", input.Permissions)
	}
}

// TestNormalizeInputRejectsInvalidFields 验证角色名称、说明和权限代码白名单。
func TestNormalizeInputRejectsInvalidFields(t *testing.T) {
	_, fields := normalizeInput(Input{
		Description: string(make([]rune, 201)),
		Permissions: []domain.PermissionCode{"inbox.view"},
	}, true)
	if fields["name"] != ValidationNameRequired {
		t.Fatalf("name validation = %q", fields["name"])
	}
	if fields["description"] != ValidationDescriptionTooLong {
		t.Fatalf("description validation = %q", fields["description"])
	}
	if fields["permissions"] != ValidationPermissionsInvalid {
		t.Fatalf("permission validation = %q", fields["permissions"])
	}
}

// TestNormalizeInputRejectsLongName 验证自定义角色名称长度上限。
func TestNormalizeInputRejectsLongName(t *testing.T) {
	_, fields := normalizeInput(Input{Name: string(make([]rune, maxRoleNameLength+1))}, true)
	if fields["name"] != ValidationNameTooLong {
		t.Fatalf("name validation = %q", fields["name"])
	}
}

// TestNormalizeInputIgnoresBuiltInMetadata 验证内置角色只校验权限。
func TestNormalizeInputIgnoresBuiltInMetadata(t *testing.T) {
	input, fields := normalizeInput(Input{
		Name:        string(make([]rune, 51)),
		Description: string(make([]rune, 201)),
		Permissions: []domain.PermissionCode{domain.PermissionTeamMembersView},
	}, false)
	if len(fields) != 0 {
		t.Fatalf("validation fields = %v, want empty", fields)
	}
	if input.Name != "" || input.Description != "" {
		t.Fatalf("built-in metadata = (%q, %q), want empty", input.Name, input.Description)
	}
}

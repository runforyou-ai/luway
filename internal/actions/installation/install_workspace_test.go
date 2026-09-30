//go:build server

package installation

import (
	"context"
	"errors"
	"strings"
	"testing"

	accountaction "github.com/runforyou-ai/luway/internal/actions/account"
	organizationaction "github.com/runforyou-ai/luway/internal/actions/organization"
	"github.com/runforyou-ai/luway/internal/domain"
)

// validInput 返回可以通过字段校验的首次安装输入。
func validInput() InstallWorkspaceInput {
	return InstallWorkspaceInput{
		WorkspaceName: "演示测试公司",
		WorkspaceSlug: "demo-test",
		DisplayName:   "管理员",
		Email:         "admin@example.com",
		Password:      "password123",
		Locale:        domain.LocaleChineseSimplified,
		TimeZone:      "Asia/Shanghai",
	}
}

// installValidationFields 执行首次安装并返回字段校验结果。
func installValidationFields(t *testing.T, input InstallWorkspaceInput) map[string]accountaction.ValidationCode {
	t.Helper()
	_, err := NewInstallWorkspaceAction(nil).Execute(context.Background(), input)
	var validationError *ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("error = %#v, want validation error", err)
	}
	return validationError.Fields
}

// TestInstallRejectsPasswordLongerThanBcryptLimit 验证首次安装拒绝超过 bcrypt 上限的密码。
func TestInstallRejectsPasswordLongerThanBcryptLimit(t *testing.T) {
	input := validInput()
	input.Password = strings.Repeat("中", 25)
	if fields := installValidationFields(t, input); fields["password"] != accountaction.ValidationPasswordTooLong {
		t.Fatalf("fields = %#v", fields)
	}
}

// TestInstallRejectsInvalidLocaleAndTimeZone 验证首次安装拒绝无效的浏览器区域设置。
func TestInstallRejectsInvalidLocaleAndTimeZone(t *testing.T) {
	input := validInput()
	input.Locale, input.TimeZone = domain.Locale("fr-FR"), "invalid"
	fields := installValidationFields(t, input)
	if fields["locale"] != accountaction.ValidationLocaleInvalid || fields["timeZone"] != accountaction.ValidationTimeZoneInvalid {
		t.Fatalf("fields = %#v", fields)
	}
}

// TestInstallRejectsInvalidWorkspace 验证首次安装校验工作区名称长度和标识格式。
func TestInstallRejectsInvalidWorkspace(t *testing.T) {
	input := validInput()
	input.WorkspaceName, input.WorkspaceSlug = strings.Repeat("名", domain.OrganizationNameMaxLength+1), "Bad_Slug"
	fields := installValidationFields(t, input)
	if fields["workspaceName"] != organizationaction.ValidationNameTooLong || fields["workspaceSlug"] != organizationaction.ValidationSlugInvalid {
		t.Fatalf("fields = %#v", fields)
	}
}

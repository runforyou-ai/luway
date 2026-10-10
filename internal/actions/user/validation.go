//go:build server

package user

import (
	"strings"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/str"
)

// ValidationCode 标识用户字段校验结果。
type ValidationCode = common.FieldCode

// 成员资料、偏好与目录查询的字段校验码。
const (
	ValidationDisplayNameRequired        ValidationCode = "USER_DISPLAY_NAME_REQUIRED"
	ValidationDisplayNameInvalid         ValidationCode = "USER_DISPLAY_NAME_INVALID"
	ValidationEmailInvalid               ValidationCode = "USER_EMAIL_INVALID"
	ValidationEmailDuplicate             ValidationCode = "USER_EMAIL_DUPLICATE"
	ValidationLocaleInvalid              ValidationCode = "USER_LOCALE_INVALID"
	ValidationTranslationLanguageInvalid ValidationCode = "USER_TRANSLATION_LANGUAGE_INVALID"
	ValidationTimeZoneInvalid            ValidationCode = "USER_TIME_ZONE_INVALID"
	ValidationRoleInvalid                ValidationCode = "USER_ROLE_INVALID"
	ValidationTeamInvalid                ValidationCode = "USER_TEAM_INVALID"
	ValidationStatusInvalid              ValidationCode = "USER_STATUS_INVALID"
	ValidationMaxServiceSessionsInvalid  ValidationCode = "USER_MAX_SERVICE_SESSIONS_INVALID"
)

// ValidationError 表示用户字段校验失败。
type ValidationError = common.FieldError

// normalizeProfileInput 规范化并校验个人资料输入。
func normalizeProfileInput(input ProfileInput) (ProfileInput, map[string]ValidationCode) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.AvatarFileID = strings.TrimSpace(input.AvatarFileID)

	fields := make(map[string]ValidationCode)
	if input.DisplayName == "" {
		fields["displayName"] = ValidationDisplayNameRequired
	} else if !domain.IdentityDisplayNameValid(input.DisplayName) {
		fields["displayName"] = ValidationDisplayNameInvalid
	}
	if !str.IsEmail(input.Email) {
		fields["email"] = ValidationEmailInvalid
	}
	return input, fields
}

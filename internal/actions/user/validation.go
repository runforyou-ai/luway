//go:build server

package user

import (
	"strings"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/str"
)

// 成员资料、偏好与目录查询的字段校验码。
const (
	ValidationDisplayNameRequired        common.FieldCode = "USER_DISPLAY_NAME_REQUIRED"
	ValidationDisplayNameInvalid         common.FieldCode = "USER_DISPLAY_NAME_INVALID"
	ValidationEmailInvalid               common.FieldCode = "USER_EMAIL_INVALID"
	ValidationEmailDuplicate             common.FieldCode = "USER_EMAIL_DUPLICATE"
	ValidationLocaleInvalid              common.FieldCode = "USER_LOCALE_INVALID"
	ValidationTranslationLanguageInvalid common.FieldCode = "USER_TRANSLATION_LANGUAGE_INVALID"
	ValidationTimeZoneInvalid            common.FieldCode = "USER_TIME_ZONE_INVALID"
	ValidationRoleInvalid                common.FieldCode = "USER_ROLE_INVALID"
	ValidationTeamInvalid                common.FieldCode = "USER_TEAM_INVALID"
	ValidationStatusInvalid              common.FieldCode = "USER_STATUS_INVALID"
	ValidationMaxServiceSessionsInvalid  common.FieldCode = "USER_MAX_SERVICE_SESSIONS_INVALID"
)

// normalizeProfileInput 规范化并校验个人资料输入。
func normalizeProfileInput(input ProfileInput) (ProfileInput, map[string]common.FieldCode) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.AvatarFileID = strings.TrimSpace(input.AvatarFileID)

	fields := make(map[string]common.FieldCode)
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

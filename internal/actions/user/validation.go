//go:build server

package user

import (
	"strings"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	commonemail "github.com/runforyou-ai/cervi/pkg/email"
)

// ValidationCode 标识用户字段校验结果。
type ValidationCode = common.FieldCode

const (
	ValidationDisplayNameRequired        ValidationCode = "USER_DISPLAY_NAME_REQUIRED"
	ValidationDisplayNameInvalid         ValidationCode = "USER_DISPLAY_NAME_INVALID"
	ValidationEmailInvalid               ValidationCode = "USER_EMAIL_INVALID"
	ValidationEmailDuplicate             ValidationCode = "USER_EMAIL_DUPLICATE"
	ValidationLocaleInvalid              ValidationCode = "USER_LOCALE_INVALID"
	ValidationTranslationLanguageInvalid ValidationCode = "USER_TRANSLATION_LANGUAGE_INVALID"
	ValidationTimeZoneInvalid            ValidationCode = "USER_TIME_ZONE_INVALID"
	ValidationWorkStatusInvalid          ValidationCode = "USER_WORK_STATUS_INVALID"
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
	input.Email = commonemail.Normalize(input.Email)
	input.AvatarFileID = strings.TrimSpace(input.AvatarFileID)

	fields := make(map[string]ValidationCode)
	if input.DisplayName == "" {
		fields["displayName"] = ValidationDisplayNameRequired
	} else if !domain.IdentityDisplayNameValid(input.DisplayName) {
		fields["displayName"] = ValidationDisplayNameInvalid
	}
	if !commonemail.Valid(input.Email) {
		fields["email"] = ValidationEmailInvalid
	}
	return input, fields
}

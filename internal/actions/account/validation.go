//go:build server

// Package account 实现平台级账号的创建、注册与凭据维护。
package account

import (
	"errors"
	_ "time/tzdata"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	commonpassword "github.com/runforyou-ai/luway/pkg/password"
	"github.com/runforyou-ai/support/str"
	"github.com/runforyou-ai/support/validate"
)

const (
	ValidationDisplayNameRequired      common.FieldCode = "ACCOUNT_DISPLAY_NAME_REQUIRED"
	ValidationDisplayNameInvalid       common.FieldCode = "ACCOUNT_DISPLAY_NAME_INVALID"
	ValidationEmailInvalid             common.FieldCode = "ACCOUNT_EMAIL_INVALID"
	ValidationEmailDuplicate           common.FieldCode = "ACCOUNT_EMAIL_DUPLICATE"
	ValidationPasswordTooShort         common.FieldCode = "ACCOUNT_PASSWORD_TOO_SHORT"
	ValidationPasswordTooLong          common.FieldCode = "ACCOUNT_PASSWORD_TOO_LONG"
	ValidationCurrentPasswordIncorrect common.FieldCode = "ACCOUNT_CURRENT_PASSWORD_INCORRECT"
	ValidationLocaleInvalid            common.FieldCode = "ACCOUNT_LOCALE_INVALID"
	ValidationTimeZoneInvalid          common.FieldCode = "ACCOUNT_TIME_ZONE_INVALID"
)

// NewAccountInput 定义新建本地账号的字段。
type NewAccountInput struct {
	DisplayName string
	Email       string
	Password    string
	Locale      domain.Locale
	TimeZone    string
}

// ValidateNewAccount 校验新建本地账号的字段，字段名与客户端表单一致。
func ValidateNewAccount(input NewAccountInput) map[string]common.FieldCode {
	fields := make(map[string]common.FieldCode)
	if input.DisplayName == "" {
		fields["displayName"] = ValidationDisplayNameRequired
	} else if !domain.IdentityDisplayNameValid(input.DisplayName) {
		fields["displayName"] = ValidationDisplayNameInvalid
	}
	if !str.IsEmail(input.Email) {
		fields["email"] = ValidationEmailInvalid
	}
	if code, ok := passwordCode(input.Password); ok {
		fields["password"] = code
	}
	if !input.Locale.Valid() {
		fields["locale"] = ValidationLocaleInvalid
	}
	if !validate.Timezone(input.TimeZone) {
		fields["timeZone"] = ValidationTimeZoneInvalid
	}
	return fields
}

// passwordCode 返回密码长度不符合要求时的校验结果。
func passwordCode(password string) (common.FieldCode, bool) {
	switch err := commonpassword.Validate(password); {
	case errors.Is(err, commonpassword.ErrTooShort):
		return ValidationPasswordTooShort, true
	case errors.Is(err, commonpassword.ErrTooLong):
		return ValidationPasswordTooLong, true
	}
	return "", false
}

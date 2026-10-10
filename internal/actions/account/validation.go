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

// ValidationCode 标识账号字段的校验结果。
type ValidationCode = common.FieldCode

const (
	ValidationDisplayNameRequired      ValidationCode = "ACCOUNT_DISPLAY_NAME_REQUIRED"
	ValidationDisplayNameInvalid       ValidationCode = "ACCOUNT_DISPLAY_NAME_INVALID"
	ValidationEmailInvalid             ValidationCode = "ACCOUNT_EMAIL_INVALID"
	ValidationEmailDuplicate           ValidationCode = "ACCOUNT_EMAIL_DUPLICATE"
	ValidationPasswordTooShort         ValidationCode = "ACCOUNT_PASSWORD_TOO_SHORT"
	ValidationPasswordTooLong          ValidationCode = "ACCOUNT_PASSWORD_TOO_LONG"
	ValidationCurrentPasswordIncorrect ValidationCode = "ACCOUNT_CURRENT_PASSWORD_INCORRECT"
	ValidationLocaleInvalid            ValidationCode = "ACCOUNT_LOCALE_INVALID"
	ValidationTimeZoneInvalid          ValidationCode = "ACCOUNT_TIME_ZONE_INVALID"
)

// ValidationError 表示账号字段校验失败。
type ValidationError = common.FieldError

// NewAccountInput 定义新建本地账号的字段。
type NewAccountInput struct {
	DisplayName string
	Email       string
	Password    string
	Locale      domain.Locale
	TimeZone    string
}

// ValidateNewAccount 校验新建本地账号的字段，字段名与客户端表单一致。
func ValidateNewAccount(input NewAccountInput) map[string]ValidationCode {
	fields := make(map[string]ValidationCode)
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
func passwordCode(password string) (ValidationCode, bool) {
	switch err := commonpassword.Validate(password); {
	case errors.Is(err, commonpassword.ErrTooShort):
		return ValidationPasswordTooShort, true
	case errors.Is(err, commonpassword.ErrTooLong):
		return ValidationPasswordTooLong, true
	}
	return "", false
}

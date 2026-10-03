//go:build server

package computer

import (
	"strings"
	"unicode/utf8"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
)

// ValidationCode 标识电脑字段校验结果。
type ValidationCode = common.FieldCode

const (
	ValidationInstallIDRequired ValidationCode = "COMPUTER_INSTALL_ID_REQUIRED"
	ValidationInstallIDTooLong  ValidationCode = "COMPUTER_INSTALL_ID_TOO_LONG"
	ValidationNameRequired      ValidationCode = "COMPUTER_NAME_REQUIRED"
	ValidationNameTooLong       ValidationCode = "COMPUTER_NAME_TOO_LONG"
	ValidationPlatformInvalid   ValidationCode = "COMPUTER_PLATFORM_INVALID"
)

// ValidationError 表示电脑字段校验失败。
type ValidationError = common.FieldError

const (
	// maxInstallIDLength 是执行器安装标识的最大字符数。
	maxInstallIDLength = 64
	// maxNameLength 是电脑名称的最大字符数。
	maxNameLength = 100
	// defaultMaxConcurrency 是执行器未给出有效上限时同时执行的操作数。
	defaultMaxConcurrency = 4
	// maxConcurrencyLimit 是同时执行的操作数上限。
	maxConcurrencyLimit = 32
)

// normalizeRegisterInput 归一化并校验电脑注册输入。
func normalizeRegisterInput(input RegisterInput) (RegisterInput, map[string]ValidationCode) {
	input.InstallID = strings.TrimSpace(input.InstallID)
	input.Name = strings.TrimSpace(input.Name)
	fields := make(map[string]ValidationCode)
	if input.InstallID == "" {
		fields["installId"] = ValidationInstallIDRequired
	} else if utf8.RuneCountInString(input.InstallID) > maxInstallIDLength {
		fields["installId"] = ValidationInstallIDTooLong
	}
	if input.Name == "" {
		fields["name"] = ValidationNameRequired
	} else if utf8.RuneCountInString(input.Name) > maxNameLength {
		fields["name"] = ValidationNameTooLong
	}
	if !domain.ValidComputerPlatform(input.Platform) {
		fields["platform"] = ValidationPlatformInvalid
	}
	return input, fields
}

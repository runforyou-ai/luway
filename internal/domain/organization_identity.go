package domain

import (
	"strings"
	"unicode"
)

// OrganizationIdentityType 定义企业身份类型。
type OrganizationIdentityType string

const (
	OrganizationIdentityTypeUser  OrganizationIdentityType = "user"
	OrganizationIdentityTypeAgent OrganizationIdentityType = "agent"
)

// IdentityDisplayNameValid 判断企业成员与 AI 员工的显示名是否只包含字母、数字、空格和 · - _ . 符号。
func IdentityDisplayNameValid(name string) bool {
	for _, char := range name {
		if !unicode.IsLetter(char) && !unicode.IsMark(char) && !unicode.IsNumber(char) && !strings.ContainsRune(" ·-_.", char) {
			return false
		}
	}
	return true
}

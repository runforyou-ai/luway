// Package email 提供邮箱地址的标准化和校验能力。
package email

import (
	"net/mail"
	"strings"
)

// Normalize 统一邮箱地址的大小写和首尾空白。
func Normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// Valid 判断邮箱地址是否符合标准格式。
func Valid(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && strings.EqualFold(address.Address, value)
}

// Package phone 提供国际电话号码的标准化和校验能力。
package phone

import (
	"strings"
	"unicode"
)

// Normalize 去除国际电话号码中的空白、短横线和括号；结果须以 + 开头并带 6 到 15 位数字，否则返回 false。
func Normalize(value string) (string, bool) {
	normalized := strings.Map(func(value rune) rune {
		if unicode.IsSpace(value) || value == '-' || value == '(' || value == ')' {
			return -1
		}
		return value
	}, strings.TrimSpace(value))
	digits := strings.TrimPrefix(normalized, "+")
	if !strings.HasPrefix(normalized, "+") || len(digits) < 6 || len(digits) > 15 {
		return "", false
	}
	for _, value := range digits {
		if value < '0' || value > '9' {
			return "", false
		}
	}
	return normalized, true
}

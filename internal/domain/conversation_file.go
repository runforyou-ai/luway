package domain

import (
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// conversationFilePathLimit 是会话文件路径的最大字符数。
	conversationFilePathLimit = 512
	// conversationFileNameLimit 是会话文件路径中每一段的最大字符数。
	conversationFileNameLimit = 255
)

// NormalizeConversationFilePath 把会话共享文件区内的路径规范为以 / 分隔、不以 / 开头、不含 . 与 .. 段的相对路径；路径为空、指向文件区根目录、越出文件区、含控制字符或超过长度上限时返回 false。
func NormalizeConversationFilePath(value string) (string, bool) {
	value = strings.TrimLeft(strings.TrimSpace(strings.ReplaceAll(value, "\\", "/")), "/")
	cleaned := path.Clean(value)
	if value == "" || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || utf8.RuneCountInString(cleaned) > conversationFilePathLimit {
		return "", false
	}
	for _, segment := range strings.Split(cleaned, "/") {
		if strings.TrimSpace(segment) != segment || utf8.RuneCountInString(segment) > conversationFileNameLimit ||
			strings.ContainsFunc(segment, unicode.IsControl) {
			return "", false
		}
	}
	return cleaned, true
}

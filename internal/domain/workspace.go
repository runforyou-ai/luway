package domain

import "strings"

// WorkspaceSlugMaxLength 是工作区标识允许的最大字符数。
const WorkspaceSlugMaxLength = 63

// NormalizeWorkspaceSlug 去除首尾空白并转为小写。
func NormalizeWorkspaceSlug(slug string) string {
	return strings.ToLower(strings.TrimSpace(slug))
}

// WorkspaceSlugValid 判断规范化后的工作区标识是否为 1 到 63 位小写字母、数字或连字符，且首尾不是连字符。
func WorkspaceSlugValid(slug string) bool {
	if slug == "" || len(slug) > WorkspaceSlugMaxLength || slug[0] == '-' || slug[len(slug)-1] == '-' {
		return false
	}
	for _, char := range slug {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

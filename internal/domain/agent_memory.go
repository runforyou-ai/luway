package domain

import (
	"strings"
	"unicode/utf8"
)

const (
	// AgentMemoryNameMaxLength 是 AI 员工记忆名称的最大字符数。
	AgentMemoryNameMaxLength = 60
	// AgentMemoryDescriptionMaxLength 是 AI 员工记忆说明的最大字符数。
	AgentMemoryDescriptionMaxLength = 200
	// AgentMemoryBodyMaxLength 是 AI 员工记忆正文的最大字符数。
	AgentMemoryBodyMaxLength = 2000
	// AgentMemoryPathMaxLength 是 AI 员工记忆条目路径的最大字符数。
	AgentMemoryPathMaxLength = 100
	// AgentMemoryIndexPath 是由系统生成的记忆索引路径，不作为条目路径。
	AgentMemoryIndexPath = "MEMORY.md"
)

// ValidAgentMemoryPath 判断条目路径是记忆目录下的单层 Markdown 文件名且不是索引文件。
func ValidAgentMemoryPath(path string) bool {
	return strings.HasSuffix(path, ".md") && len(path) > len(".md") &&
		utf8.RuneCountInString(path) <= AgentMemoryPathMaxLength &&
		!strings.ContainsAny(path, "/\\") && !strings.HasPrefix(path, ".") &&
		!strings.EqualFold(path, AgentMemoryIndexPath)
}

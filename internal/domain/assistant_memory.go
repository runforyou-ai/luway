package domain

import (
	"strings"
	"unicode/utf8"
)

const (
	// AssistantMemoryNameMaxLength 是助理记忆名称的最大字符数。
	AssistantMemoryNameMaxLength = 60
	// AssistantMemoryDescriptionMaxLength 是助理记忆说明的最大字符数。
	AssistantMemoryDescriptionMaxLength = 200
	// AssistantMemoryBodyMaxLength 是助理记忆正文的最大字符数。
	AssistantMemoryBodyMaxLength = 2000
	// AssistantMemoryPathMaxLength 是助理记忆条目路径的最大字符数。
	AssistantMemoryPathMaxLength = 100
	// AssistantMemoryIndexPath 是由系统生成的记忆索引路径，不作为条目路径。
	AssistantMemoryIndexPath = "MEMORY.md"
)

// ValidAssistantMemoryPath 判断条目路径是记忆目录下的单层 Markdown 文件名且不是索引文件。
func ValidAssistantMemoryPath(path string) bool {
	return strings.HasSuffix(path, ".md") && len(path) > len(".md") &&
		utf8.RuneCountInString(path) <= AssistantMemoryPathMaxLength &&
		!strings.ContainsAny(path, "/\\") && !strings.HasPrefix(path, ".") &&
		!strings.EqualFold(path, AssistantMemoryIndexPath)
}

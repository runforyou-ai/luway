//go:build server

package knowledgebase

import "github.com/runforyou-ai/luway/internal/integration/documentsplit"

// segment 是写入知识分段表的一段正文。
type segment = documentsplit.Segment

// splitSegments 按 Markdown 结构切分正文。
func splitSegments(text string, length, overlap int) ([]segment, error) {
	return documentsplit.Split(text, length, overlap)
}

// indexText 返回上下文与正文拼接后用于向量化、词法检索和重排的文本，上下文非空时以空行分隔置于正文之前。
func indexText(context, content string) string {
	if context == "" {
		return content
	}
	return context + "\n\n" + content
}

//go:build server

package knowledgebase

import (
	"strings"

	"github.com/runforyou-ai/mdchunk/split"
)

// segment 是写入知识分段表的一段正文：批次内从 1 开始的位置、字符数、标题路径与表头组成的上下文和正文。
type segment struct {
	Position       int
	CharacterCount int
	Context        string
	Content        string
}

// splitSegments 把换行统一为 LF 后按 Markdown 结构以目标长度和重叠切分正文，位置从 1 开始连续编号。
func splitSegments(text string, length, overlap int) ([]segment, error) {
	splitter, err := split.New(split.Options{Size: length, Overlap: overlap})
	if err != nil {
		return nil, err
	}
	chunks := splitter.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"))
	segments := make([]segment, 0, len(chunks))
	for i, chunk := range chunks {
		segments = append(segments, segment{Position: i + 1, CharacterCount: chunk.Length, Context: chunk.Context(), Content: chunk.Text})
	}
	return segments, nil
}

// indexText 返回上下文与正文拼接后用于向量化、词法检索和重排的文本，上下文非空时以空行分隔置于正文之前。
func indexText(context, content string) string {
	if context == "" {
		return content
	}
	return context + "\n\n" + content
}

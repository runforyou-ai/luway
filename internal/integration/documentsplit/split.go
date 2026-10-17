//go:build server

// Package documentsplit 按 Markdown 结构切分正文，切分由 mdchunk 完成。
package documentsplit

import (
	"strings"

	"github.com/runforyou-ai/mdchunk/split"
)

// Segment 是切分后的一段正文：批次内从 1 开始的位置、字符数、标题路径与表头组成的上下文和正文。
type Segment struct {
	Position       int
	CharacterCount int
	Context        string
	Content        string
}

// Split 把换行统一为 LF 后按 Markdown 结构以目标长度和重叠切分正文，位置从 1 开始连续编号。
func Split(text string, length, overlap int) ([]Segment, error) {
	splitter, err := split.New(split.Options{Size: length, Overlap: overlap})
	if err != nil {
		return nil, err
	}
	chunks := splitter.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"))
	segments := make([]Segment, 0, len(chunks))
	for i, chunk := range chunks {
		segments = append(segments, Segment{Position: i + 1, CharacterCount: chunk.Length, Context: chunk.Context(), Content: chunk.Text})
	}
	return segments, nil
}

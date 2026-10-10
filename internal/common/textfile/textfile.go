// Package textfile 提供 AI 员工文件工具共用的文本读取、摘要校验与原文替换规则，电脑上的文件与会话共享文件区一致使用。
package textfile

import (
	"errors"
	"fmt"
	"strings"

	"github.com/runforyou-ai/support/random"
)

const (
	// MaxReadBytes 是按文本读取单个文件的字节上限。
	MaxReadBytes = 10 << 20
	// defaultReadLines 是未指定行数时读取的行数上限。
	defaultReadLines = 2000
)

// Hash 返回文件内容的 SHA-256 摘要。
func Hash(content []byte) string {
	return random.SHA256Hex(content)
}

// Numbered 按行返回以 cat -n 格式带行号的内容，offset 从 1 开始，limit 为 0 时最多 2000 行；文件为空或起始行越过末尾时返回说明。
func Numbered(content []byte, offset, limit int) string {
	lines := strings.SplitAfter(string(content), "\n")
	start := max(offset, 1) - 1
	if limit <= 0 {
		limit = defaultReadLines
	}
	if len(content) == 0 || start >= len(lines) {
		return fmt.Sprintf("没有读到内容：文件为空，或第 %d 行超过了文件末尾。", start+1)
	}
	end := min(len(lines), start+limit)
	var text strings.Builder
	for index, line := range lines[start:end] {
		line = strings.TrimSuffix(line, "\n")
		if index > 0 {
			text.WriteByte('\n')
		}
		fmt.Fprintf(&text, "%6d\t%s", start+index+1, line)
	}
	return text.String()
}

// CheckBase 校验已有文件的当前内容与读取时的摘要一致：没有读取记录或内容已变化时返回要求重新读取的错误，name 是错误中展示的路径。
func CheckBase(name string, current []byte, baseHash string) error {
	if baseHash == "" {
		return fmt.Errorf("文件已存在，覆盖或修改前先用 read_file 读取：%s", name)
	}
	if Hash(current) != baseHash {
		return fmt.Errorf("文件在读取后已被改动，请重新用 read_file 读取后再改：%s", name)
	}
	return nil
}

// Replace 把文本中的原文替换为新内容：原文不能为空且必须存在，未要求全部替换时必须唯一，name 是错误中展示的路径。
func Replace(name, text, oldString, newString string, replaceAll bool) (string, error) {
	if oldString == "" {
		return "", errors.New("要替换的原文不能为空")
	}
	if oldString == newString {
		return "", errors.New("替换内容与原文相同")
	}
	count := strings.Count(text, oldString)
	switch {
	case count == 0:
		return "", fmt.Errorf("文件中找不到要替换的原文：%s", name)
	case count > 1 && !replaceAll:
		return "", fmt.Errorf("原文在文件中出现 %d 次，请提供更多上下文使其唯一，或设置 replace_all：%s", count, name)
	}
	if replaceAll {
		return strings.ReplaceAll(text, oldString, newString), nil
	}
	return strings.Replace(text, oldString, newString, 1), nil
}

// Package languagetag 规范化并比较 BCP 47 语言标签。
package languagetag

import (
	"strings"

	"golang.org/x/text/language"
)

// Undetermined 表示文本没有可识别的语言内容。
const Undetermined = "und"

// Normalize 返回规范形式的语言标签；标签无效或超过 35 个字符时返回 false。
func Normalize(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 35 {
		return "", false
	}
	tag, err := language.Parse(value)
	if err != nil {
		return "", false
	}
	return tag.String(), true
}

// Same 判断两个语言标签是否为同一种书面语言：主语言与书写系统都相同，书写系统未写明时按该语言与地区的常用书写系统推断，忽略地区。
func Same(left, right string) bool {
	leftLanguage, leftScript := written(left)
	rightLanguage, rightScript := written(right)
	return leftLanguage != "" && leftLanguage == rightLanguage && leftScript == rightScript
}

// Readable 判断读者语言无需翻译即可阅读该文本语言：同一语言或无语言内容。
func Readable(text, reader string) bool {
	return base(text) == Undetermined || Same(text, reader)
}

// written 返回语言标签的主语言与书写系统，无法解析时为空。
func written(value string) (string, string) {
	tag, err := language.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", ""
	}
	primary, _, _ := tag.Raw()
	script, _ := tag.Script()
	return primary.String(), script.String()
}

// base 返回语言标签的主语言子标签，无法解析时为空。
func base(value string) string {
	tag, err := language.Parse(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	primary, _, _ := tag.Raw()
	return primary.String()
}

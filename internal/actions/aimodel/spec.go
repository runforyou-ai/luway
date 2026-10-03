//go:build server

package aimodel

import (
	"strings"
	"unicode/utf8"

	"github.com/runforyou-ai/luway/internal/domain"
)

const (
	// maxNameLength 是模型名称的最大字符数。
	maxNameLength = 200
	// maxIdentifierLength 是上游模型标识的最大字符数。
	maxIdentifierLength = 200
)

// ValidIdentifier 校验来源的上游模型标识非空且不超过长度上限。
func ValidIdentifier(identifier string) bool {
	return identifier != "" && utf8.RuneCountInString(identifier) <= maxIdentifierLength
}

// Spec 定义模型自身的属性：名称、类型、输入模态、上下文窗口与最大输出 Token 数。
type Spec struct {
	Name            string
	Type            domain.AIModelType
	InputModalities []domain.AIModelInputModality
	ContextWindow   int64
	MaxOutputTokens int64
}

// NormalizeSpec 规范化并校验模型属性：名称去除首尾空白，非对话模型的最大输出 Token 数置零；返回首个无效字段名，全部有效时为空。
func NormalizeSpec(spec *Spec) string {
	spec.Name = strings.TrimSpace(spec.Name)
	switch {
	case spec.Name == "" || utf8.RuneCountInString(spec.Name) > maxNameLength:
		return "name"
	case spec.Type != domain.AIModelTypeChat && spec.Type != domain.AIModelTypeEmbedding &&
		spec.Type != domain.AIModelTypeRerank && spec.Type != domain.AIModelTypeDecision:
		return "type"
	case !validInputModalities(spec.InputModalities):
		return "inputModalities"
	case spec.ContextWindow <= 0:
		return "contextWindow"
	case spec.Type != domain.AIModelTypeChat:
		spec.MaxOutputTokens = 0
	case spec.MaxOutputTokens <= 0:
		return "maxOutputTokens"
	}
	return ""
}

// validInputModalities 校验模型至少声明一种且不重复的输入模态。
func validInputModalities(modalities []domain.AIModelInputModality) bool {
	if len(modalities) == 0 {
		return false
	}
	seen := make(map[domain.AIModelInputModality]struct{}, len(modalities))
	for _, modality := range modalities {
		switch modality {
		case domain.AIModelInputModalityText,
			domain.AIModelInputModalityImage,
			domain.AIModelInputModalityAudio,
			domain.AIModelInputModalityVideo:
		default:
			return false
		}
		if _, exists := seen[modality]; exists {
			return false
		}
		seen[modality] = struct{}{}
	}
	return true
}

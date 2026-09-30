//go:build server

package aiprovider

import "github.com/runforyou-ai/cervi/internal/domain"

// AvailableModels 返回指定品牌的预设模型目录。
func AvailableModels(brand domain.AIProviderBrand) []Model {
	text := []domain.AIModelInputModality{domain.AIModelInputModalityText}
	textAndImage := []domain.AIModelInputModality{domain.AIModelInputModalityText, domain.AIModelInputModalityImage}
	textImageVideo := []domain.AIModelInputModality{
		domain.AIModelInputModalityText,
		domain.AIModelInputModalityImage,
		domain.AIModelInputModalityVideo,
	}
	textImageAudioVideo := []domain.AIModelInputModality{
		domain.AIModelInputModalityText,
		domain.AIModelInputModalityImage,
		domain.AIModelInputModalityAudio,
		domain.AIModelInputModalityVideo,
	}
	switch brand {
	case domain.AIProviderBrandDeepSeek:
		return []Model{
			{Identifier: "deepseek-flash", Name: "DeepSeek V4.1 Flash", Type: domain.AIModelTypeChat, InputModalities: textAndImage, ContextWindow: 1_048_576, MaxOutputTokens: 393_216},
		}
	case domain.AIProviderBrandAlibaba:
		return []Model{
			{Identifier: "qwen3.8-max", Name: "Qwen 3.8 Max", Type: domain.AIModelTypeChat, InputModalities: textImageVideo, ContextWindow: 1_000_000, MaxOutputTokens: 131_072},
			{Identifier: "qwen3.7-plus", Name: "Qwen 3.7 Plus", Type: domain.AIModelTypeChat, InputModalities: textImageVideo, ContextWindow: 1_000_000, MaxOutputTokens: 131_072},
			{Identifier: "qwen3.8-flash", Name: "Qwen 3.8 Flash", Type: domain.AIModelTypeChat, InputModalities: textImageVideo, ContextWindow: 1_000_000, MaxOutputTokens: 131_072},
			{Identifier: "qwen3.7-text-embedding", Name: "Qwen 3.7 Text Embedding", Type: domain.AIModelTypeEmbedding, InputModalities: text, ContextWindow: 131_072},
			{Identifier: "qwen3-vl-embedding", Name: "Qwen 3 VL Embedding", Type: domain.AIModelTypeEmbedding, InputModalities: textAndImage, ContextWindow: 32_000},
			{Identifier: "qwen3.7-text-rerank", Name: "Qwen 3.7 Text Rerank", Type: domain.AIModelTypeRerank, InputModalities: text, ContextWindow: 30_000},
			{Identifier: "qwen3-vl-rerank", Name: "Qwen 3 VL Rerank", Type: domain.AIModelTypeRerank, InputModalities: textImageVideo, ContextWindow: 8_000},
		}
	case domain.AIProviderBrandOpenAI:
		return []Model{
			{Identifier: "gpt-6-astra", Name: "GPT-6 Astra", Type: domain.AIModelTypeChat, InputModalities: textAndImage, ContextWindow: 1_050_000, MaxOutputTokens: 128_000},
			{Identifier: "gpt-6-sol", Name: "GPT-6 Sol", Type: domain.AIModelTypeChat, InputModalities: textAndImage, ContextWindow: 1_050_000, MaxOutputTokens: 128_000},
			{Identifier: "gpt-6-luna", Name: "GPT-6 Luna", Type: domain.AIModelTypeChat, InputModalities: textAndImage, ContextWindow: 1_050_000, MaxOutputTokens: 128_000},
			{Identifier: "text-embedding-3-large", Name: "Text Embedding 3 Large", Type: domain.AIModelTypeEmbedding, InputModalities: text, ContextWindow: 8_192},
			{Identifier: "text-embedding-3-small", Name: "Text Embedding 3 Small", Type: domain.AIModelTypeEmbedding, InputModalities: text, ContextWindow: 8_192},
		}
	case domain.AIProviderBrandAnthropic:
		return []Model{
			{Identifier: "claude-fable-5-1", Name: "Claude Fable 5.1", Type: domain.AIModelTypeChat, InputModalities: textAndImage, ContextWindow: 1_000_000, MaxOutputTokens: 128_000},
			{Identifier: "claude-opus-5-5", Name: "Claude Opus 5.5", Type: domain.AIModelTypeChat, InputModalities: textAndImage, ContextWindow: 1_000_000, MaxOutputTokens: 128_000},
			{Identifier: "claude-sonnet-5", Name: "Claude Sonnet 5", Type: domain.AIModelTypeChat, InputModalities: textAndImage, ContextWindow: 1_000_000, MaxOutputTokens: 128_000},
		}
	case domain.AIProviderBrandGoogle:
		return []Model{
			{Identifier: "gemini-3.8-flash", Name: "Gemini 3.8 Flash", Type: domain.AIModelTypeChat, InputModalities: textImageAudioVideo, ContextWindow: 1_048_576, MaxOutputTokens: 65_536},
			{Identifier: "gemini-3.5-flash-lite", Name: "Gemini 3.5 Flash-Lite", Type: domain.AIModelTypeChat, InputModalities: textImageAudioVideo, ContextWindow: 1_048_576, MaxOutputTokens: 65_536},
		}
	case domain.AIProviderBrandMoonshot:
		return []Model{
			{Identifier: "kimi-k3", Name: "Kimi K3", Type: domain.AIModelTypeChat, InputModalities: textAndImage, ContextWindow: 1_048_576, MaxOutputTokens: 131_072},
		}
	case domain.AIProviderBrandZhipu:
		return []Model{
			{Identifier: "glm-5.3", Name: "GLM-5.3", Type: domain.AIModelTypeChat, InputModalities: text, ContextWindow: 1_048_576, MaxOutputTokens: 131_072},
			{Identifier: "glm-5.3-flash", Name: "GLM-5.3-Flash", Type: domain.AIModelTypeChat, InputModalities: textImageVideo, ContextWindow: 1_048_576, MaxOutputTokens: 131_072},
		}
	case domain.AIProviderBrandVolcengine:
		return []Model{
			{Identifier: "doubao-seed-evolving", Name: "Doubao Seed Evolving", Type: domain.AIModelTypeChat, InputModalities: textImageVideo, ContextWindow: 1_048_576, MaxOutputTokens: 262_144},
			{Identifier: "doubao-seed-2-1-pro-260915", Name: "Doubao Seed 2.1 Pro", Type: domain.AIModelTypeChat, InputModalities: textImageVideo, ContextWindow: 1_048_576, MaxOutputTokens: 262_144},
			{Identifier: "doubao-seed-2-1-lite-260915", Name: "Doubao Seed 2.1 Lite", Type: domain.AIModelTypeChat, InputModalities: textImageVideo, ContextWindow: 1_048_576, MaxOutputTokens: 262_144},
			{Identifier: "doubao-seed-2-1-turbo-260628", Name: "Doubao Seed 2.1 Turbo", Type: domain.AIModelTypeChat, InputModalities: textImageVideo, ContextWindow: 262_144, MaxOutputTokens: 262_144},
		}
	case domain.AIProviderBrandMiniMax:
		return []Model{
			{Identifier: "MiniMax-M3", Name: "MiniMax M3", Type: domain.AIModelTypeChat, InputModalities: textImageVideo, ContextWindow: 1_000_000, MaxOutputTokens: 131_072},
		}
	case domain.AIProviderBrandXAI:
		return []Model{
			{Identifier: "grok-4.7", Name: "Grok 4.7", Type: domain.AIModelTypeChat, InputModalities: textAndImage, ContextWindow: 500_000, MaxOutputTokens: 131_072},
		}
	case domain.AIProviderBrandMistral:
		return []Model{
			{Identifier: "mistral-medium-3-5", Name: "Mistral Medium 3.5", Type: domain.AIModelTypeChat, InputModalities: textAndImage, ContextWindow: 262_144, MaxOutputTokens: 262_144},
			{Identifier: "mistral-small-2603", Name: "Mistral Small 4", Type: domain.AIModelTypeChat, InputModalities: textAndImage, ContextWindow: 262_144, MaxOutputTokens: 262_144},
			{Identifier: "mistral-embed", Name: "Mistral Embed", Type: domain.AIModelTypeEmbedding, InputModalities: text, ContextWindow: 8_192},
			{Identifier: "codestral-embed-2505", Name: "Codestral Embed", Type: domain.AIModelTypeEmbedding, InputModalities: text, ContextWindow: 8_192},
		}
	case domain.AIProviderBrandTypeSafe:
		return []Model{
			{Identifier: "jev-latest", Name: "Jev", Type: domain.AIModelTypeDecision, InputModalities: text, ContextWindow: 32_000},
		}
	default:
		return nil
	}
}

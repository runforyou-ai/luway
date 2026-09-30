//go:build server

package aiprovider

import (
	"testing"

	"github.com/runforyou-ai/cervi/internal/domain"
)

// TestNormalizeInputAcceptsCustomModels 验证自定义模型可保存并规范化文本字段。
func TestNormalizeInputAcceptsCustomModels(t *testing.T) {
	input, fields := normalizeInput(Input{
		Brand:          domain.AIProviderBrandDeepSeek,
		Name:           " 自定义供应商 ",
		CredentialType: domain.AIProviderCredentialTypeAPIKey,
		APIKey:         " secret ",
		APIURL:         " https://api.deepseek.com ",
		Models: []Model{{
			Identifier: " custom-model ", Name: " 自定义模型 ", Type: domain.AIModelTypeChat,
			InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText},
			ContextWindow:   128_000, MaxOutputTokens: 8_000,
		}},
	})
	if len(fields) != 0 {
		t.Fatalf("normalizeInput() fields = %#v", fields)
	}
	if input.Name != "自定义供应商" || input.APIKey != "secret" || input.APIURL != "https://api.deepseek.com" {
		t.Fatalf("normalizeInput() = %#v", input)
	}
	if len(input.Models) != 1 || input.Models[0].Identifier != "custom-model" || input.Models[0].Name != "自定义模型" {
		t.Fatalf("normalizeInput() models = %#v", input.Models)
	}
}

// TestNormalizeInputRejectsInvalidModels 验证重复标识和无效 Token 数会被拒绝。
func TestNormalizeInputRejectsInvalidModels(t *testing.T) {
	_, fields := normalizeInput(Input{
		Brand: domain.AIProviderBrandDeepSeek, Name: "供应商", CredentialType: domain.AIProviderCredentialTypeAPIKey,
		APIKey: "secret", APIURL: "https://api.deepseek.com",
		Models: []Model{
			{Identifier: "model", Name: "模型一", Type: domain.AIModelTypeChat, InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText}, ContextWindow: 1, MaxOutputTokens: 1},
			{Identifier: "model", Name: "模型二", Type: domain.AIModelTypeChat, InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText}, ContextWindow: 0, MaxOutputTokens: 1},
		},
	})
	if fields["models"] != ValidationModelsInvalid {
		t.Fatalf("normalizeInput() fields = %#v", fields)
	}
}

// TestNormalizeInputRejectsEmptyModels 验证模型目录为空时不允许保存供应商。
func TestNormalizeInputRejectsEmptyModels(t *testing.T) {
	_, fields := normalizeInput(Input{
		Brand: domain.AIProviderBrandDeepSeek, Name: "供应商", CredentialType: domain.AIProviderCredentialTypeAPIKey,
		APIKey: "secret", APIURL: "https://api.deepseek.com",
	})
	if fields["models"] != ValidationModelsInvalid {
		t.Fatalf("normalizeInput() fields = %#v", fields)
	}
}

// TestNormalizeInputAcceptsEmbeddingModalities 验证嵌入模型可声明输入模态且不保存输出 Token 数。
func TestNormalizeInputAcceptsEmbeddingModalities(t *testing.T) {
	input, fields := normalizeInput(Input{
		Brand: domain.AIProviderBrandOpenAI, Name: "OpenAI", CredentialType: domain.AIProviderCredentialTypeAPIKey,
		APIKey: "secret", APIURL: "https://api.openai.com/v1",
		Models: []Model{{
			Identifier: "text-embedding-3-large", Name: "Text Embedding 3 Large", Type: domain.AIModelTypeEmbedding,
			InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText},
			ContextWindow:   8_192, MaxOutputTokens: 1,
		}},
	})
	if len(fields) != 0 {
		t.Fatalf("normalizeInput() fields = %#v", fields)
	}
	if input.Models[0].MaxOutputTokens != 0 || len(input.Models[0].InputModalities) != 1 {
		t.Fatalf("normalizeInput() model = %#v", input.Models[0])
	}
}

// TestNormalizeInputCredentialTypes 验证无凭据只对自建或本机服务成立，并清空已填写的密钥。
func TestNormalizeInputCredentialTypes(t *testing.T) {
	localModels := []Model{{
		Identifier: "qwen3:8b", Name: "qwen3:8b", Type: domain.AIModelTypeChat,
		InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText},
		ContextWindow:   32_768, MaxOutputTokens: 32_768,
	}}
	input, fields := normalizeInput(Input{
		Brand: domain.AIProviderBrandOllama, Name: "本机 Ollama", CredentialType: domain.AIProviderCredentialTypeNone,
		APIKey: "unused", APIURL: "http://localhost:11434", Models: localModels,
	})
	if len(fields) != 0 {
		t.Fatalf("normalizeInput() fields = %#v", fields)
	}
	if input.APIKey != "" {
		t.Fatalf("normalizeInput() apiKey = %q", input.APIKey)
	}

	_, fields = normalizeInput(Input{
		Brand: domain.AIProviderBrandDeepSeek, Name: "供应商", CredentialType: domain.AIProviderCredentialTypeNone,
		APIURL: "https://api.deepseek.com", Models: localModels,
	})
	if fields["credentialType"] != ValidationCredentialTypeInvalid {
		t.Fatalf("normalizeInput() fields = %#v", fields)
	}

	_, fields = normalizeInput(Input{
		Brand: domain.AIProviderBrandOpenAICompatible, Name: "自建服务", CredentialType: domain.AIProviderCredentialTypeAPIKey,
		APIURL: "http://127.0.0.1:8000/v1", Models: localModels,
	})
	if fields["apiKey"] != ValidationAPIKeyRequired {
		t.Fatalf("normalizeInput() fields = %#v", fields)
	}
}

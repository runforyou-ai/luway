package appservice

import "github.com/runforyou-ai/cervi/internal/domain"

// AIProviderBrand 表示模型服务供应商品牌。
type AIProviderBrand string

const (
	AIProviderBrandDeepSeek   AIProviderBrand = AIProviderBrand(domain.AIProviderBrandDeepSeek)
	AIProviderBrandAlibaba    AIProviderBrand = AIProviderBrand(domain.AIProviderBrandAlibaba)
	AIProviderBrandOpenAI     AIProviderBrand = AIProviderBrand(domain.AIProviderBrandOpenAI)
	AIProviderBrandAnthropic  AIProviderBrand = AIProviderBrand(domain.AIProviderBrandAnthropic)
	AIProviderBrandGoogle     AIProviderBrand = AIProviderBrand(domain.AIProviderBrandGoogle)
	AIProviderBrandMoonshot   AIProviderBrand = AIProviderBrand(domain.AIProviderBrandMoonshot)
	AIProviderBrandZhipu      AIProviderBrand = AIProviderBrand(domain.AIProviderBrandZhipu)
	AIProviderBrandVolcengine AIProviderBrand = AIProviderBrand(domain.AIProviderBrandVolcengine)
	AIProviderBrandMiniMax    AIProviderBrand = AIProviderBrand(domain.AIProviderBrandMiniMax)
	AIProviderBrandXAI        AIProviderBrand = AIProviderBrand(domain.AIProviderBrandXAI)
	AIProviderBrandMistral    AIProviderBrand = AIProviderBrand(domain.AIProviderBrandMistral)
	AIProviderBrandOpenRouter AIProviderBrand = AIProviderBrand(domain.AIProviderBrandOpenRouter)
	AIProviderBrandTypeSafe   AIProviderBrand = AIProviderBrand(domain.AIProviderBrandTypeSafe)

	// 以下品牌为自建或本机部署的模型服务，可以不配置凭据。
	AIProviderBrandOllama           AIProviderBrand = AIProviderBrand(domain.AIProviderBrandOllama)
	AIProviderBrandOpenAICompatible AIProviderBrand = AIProviderBrand(domain.AIProviderBrandOpenAICompatible)
)

// AIProviderCredentialType 表示访问模型服务所需的凭据类型。
type AIProviderCredentialType string

const (
	AIProviderCredentialTypeAPIKey AIProviderCredentialType = AIProviderCredentialType(domain.AIProviderCredentialTypeAPIKey)
	AIProviderCredentialTypeNone   AIProviderCredentialType = AIProviderCredentialType(domain.AIProviderCredentialTypeNone)
)

// AIModelType 表示 AI 模型用途。
type AIModelType string

const (
	AIModelTypeChat      AIModelType = AIModelType(domain.AIModelTypeChat)
	AIModelTypeEmbedding AIModelType = AIModelType(domain.AIModelTypeEmbedding)
	AIModelTypeRerank    AIModelType = AIModelType(domain.AIModelTypeRerank)
	AIModelTypeDecision  AIModelType = AIModelType(domain.AIModelTypeDecision)
)

// AIModelInputModality 表示模型支持的输入模态。
type AIModelInputModality string

const (
	AIModelInputModalityText  AIModelInputModality = AIModelInputModality(domain.AIModelInputModalityText)
	AIModelInputModalityImage AIModelInputModality = AIModelInputModality(domain.AIModelInputModalityImage)
	AIModelInputModalityAudio AIModelInputModality = AIModelInputModality(domain.AIModelInputModalityAudio)
	AIModelInputModalityVideo AIModelInputModality = AIModelInputModality(domain.AIModelInputModalityVideo)
)

// AIProviderInput 定义创建模型服务供应商的字段。
type AIProviderInput struct {
	Brand          AIProviderBrand          `json:"brand"`
	Name           string                   `json:"name"`
	CredentialType AIProviderCredentialType `json:"credentialType"`
	APIKey         string                   `json:"apiKey"`
	APIURL         string                   `json:"apiUrl"`
	Models         []AIProviderModel        `json:"models"`
}

// AIProviderUpdateInput 定义修改模型服务供应商的字段，品牌沿用创建时的值。
type AIProviderUpdateInput struct {
	Name           string                   `json:"name"`
	CredentialType AIProviderCredentialType `json:"credentialType"`
	APIKey         string                   `json:"apiKey"`
	APIURL         string                   `json:"apiUrl"`
	Models         []AIProviderModel        `json:"models"`
}

// AIProviderConnectionInput 定义测试模型服务连接和发现模型需要的草稿配置。
type AIProviderConnectionInput struct {
	Brand          AIProviderBrand          `json:"brand"`
	CredentialType AIProviderCredentialType `json:"credentialType"`
	APIKey         string                   `json:"apiKey"`
	APIURL         string                   `json:"apiUrl"`
}

// AIProviderModel 定义模型服务供应商的模型目录项。
type AIProviderModel struct {
	Identifier      string                 `json:"identifier"`
	Name            string                 `json:"name"`
	Type            AIModelType            `json:"type"`
	InputModalities []AIModelInputModality `json:"inputModalities"`
	ContextWindow   int64                  `json:"contextWindow"`
	MaxOutputTokens int64                  `json:"maxOutputTokens"`
}

// AIProvider 定义企业模型服务供应商及其模型目录。
type AIProvider struct {
	ID             string                   `json:"id"`
	Brand          AIProviderBrand          `json:"brand"`
	Name           string                   `json:"name"`
	CredentialType AIProviderCredentialType `json:"credentialType"`
	APIKey         string                   `json:"apiKey"`
	APIURL         string                   `json:"apiUrl"`
	Models         []AIProviderModel        `json:"models"`
}

// AIProviderModelSummary 定义供应商列表中的模型目录摘要。
type AIProviderModelSummary struct {
	Identifier string      `json:"identifier"`
	Name       string      `json:"name"`
	Type       AIModelType `json:"type"`
}

// AIProviderSummary 定义模型服务供应商列表项。
type AIProviderSummary struct {
	ID     string                   `json:"id"`
	Brand  AIProviderBrand          `json:"brand"`
	Name   string                   `json:"name"`
	APIURL string                   `json:"apiUrl"`
	Models []AIProviderModelSummary `json:"models"`
}

// AIProviderList 定义模型服务供应商列表。
type AIProviderList struct {
	Providers []AIProviderSummary `json:"providers"`
}

// AIProviderModelList 定义预设或从服务实例发现的模型目录。
type AIProviderModelList struct {
	Models []AIProviderModel `json:"models"`
}

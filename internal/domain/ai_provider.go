package domain

// AIProviderBrand 定义模型服务供应商品牌。
type AIProviderBrand string

const (
	AIProviderBrandDeepSeek         AIProviderBrand = "deepseek"
	AIProviderBrandAlibaba          AIProviderBrand = "alibaba"
	AIProviderBrandOpenAI           AIProviderBrand = "openai"
	AIProviderBrandAnthropic        AIProviderBrand = "anthropic"
	AIProviderBrandGoogle           AIProviderBrand = "google"
	AIProviderBrandMoonshot         AIProviderBrand = "moonshot"
	AIProviderBrandZhipu            AIProviderBrand = "zhipu"
	AIProviderBrandVolcengine       AIProviderBrand = "volcengine"
	AIProviderBrandMiniMax          AIProviderBrand = "minimax"
	AIProviderBrandXAI              AIProviderBrand = "xai"
	AIProviderBrandMistral          AIProviderBrand = "mistral"
	AIProviderBrandOpenRouter       AIProviderBrand = "openrouter"
	AIProviderBrandTypeSafe         AIProviderBrand = "typesafe"
	AIProviderBrandOllama           AIProviderBrand = "ollama"
	AIProviderBrandOpenAICompatible AIProviderBrand = "openai_compatible"
)

// ValidAIProviderBrand 判断模型服务供应商品牌是否为已支持取值。
func ValidAIProviderBrand(brand AIProviderBrand) bool {
	switch brand {
	case AIProviderBrandDeepSeek, AIProviderBrandAlibaba, AIProviderBrandOpenAI,
		AIProviderBrandAnthropic, AIProviderBrandGoogle, AIProviderBrandMoonshot,
		AIProviderBrandZhipu, AIProviderBrandVolcengine, AIProviderBrandMiniMax,
		AIProviderBrandXAI, AIProviderBrandMistral, AIProviderBrandOpenRouter,
		AIProviderBrandTypeSafe, AIProviderBrandOllama,
		AIProviderBrandOpenAICompatible:
		return true
	default:
		return false
	}
}

// AIProviderCredentialType 定义访问模型服务所需的凭据类型。
type AIProviderCredentialType string

const (
	AIProviderCredentialTypeAPIKey AIProviderCredentialType = "api_key"
	AIProviderCredentialTypeNone   AIProviderCredentialType = "none"
)

// ValidAIProviderCredentialType 判断凭据类型是否为已支持取值。
func ValidAIProviderCredentialType(credentialType AIProviderCredentialType) bool {
	switch credentialType {
	case AIProviderCredentialTypeAPIKey, AIProviderCredentialTypeNone:
		return true
	default:
		return false
	}
}

// AIProviderBrandSupportsNoCredential 判断品牌是否允许不配置凭据，仅自建或本机部署的服务适用。
func AIProviderBrandSupportsNoCredential(brand AIProviderBrand) bool {
	return brand == AIProviderBrandOllama || brand == AIProviderBrandOpenAICompatible
}

// AIModelType 定义 AI 模型用途。
type AIModelType string

const (
	AIModelTypeChat      AIModelType = "chat"
	AIModelTypeEmbedding AIModelType = "embedding"
	AIModelTypeRerank    AIModelType = "rerank"
	AIModelTypeDecision  AIModelType = "decision"
)

// AIModelReference 指向企业模型服务中的一个模型。
type AIModelReference struct {
	ProviderID      string
	ModelIdentifier string
}

// AIModelInputModality 定义模型支持的输入模态。
type AIModelInputModality string

const (
	AIModelInputModalityText  AIModelInputModality = "text"
	AIModelInputModalityImage AIModelInputModality = "image"
	AIModelInputModalityAudio AIModelInputModality = "audio"
	AIModelInputModalityVideo AIModelInputModality = "video"
)

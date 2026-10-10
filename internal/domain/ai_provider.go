package domain

// AIProviderBrand 定义模型服务供应商品牌。
type AIProviderBrand string

const (
	AIProviderBrandDeepSeek   AIProviderBrand = "deepseek"
	AIProviderBrandAlibaba    AIProviderBrand = "alibaba"
	AIProviderBrandOpenAI     AIProviderBrand = "openai"
	AIProviderBrandAnthropic  AIProviderBrand = "anthropic"
	AIProviderBrandGoogle     AIProviderBrand = "google"
	AIProviderBrandMoonshot   AIProviderBrand = "moonshot"
	AIProviderBrandZhipu      AIProviderBrand = "zhipu"
	AIProviderBrandVolcengine AIProviderBrand = "volcengine"
	AIProviderBrandMiniMax    AIProviderBrand = "minimax"
	AIProviderBrandXAI        AIProviderBrand = "xai"
	AIProviderBrandMistral    AIProviderBrand = "mistral"
	AIProviderBrandOpenRouter AIProviderBrand = "openrouter"
	AIProviderBrandTypeSafe   AIProviderBrand = "typesafe"
	// 以下品牌为自建或本机部署的模型服务，可以不配置凭据。
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

// AIModelScope 定义模型范围：平台模型由平台提供并对全部工作区可用，工作区模型只属于所在工作区。
type AIModelScope string

const (
	AIModelScopePlatform  AIModelScope = "platform"
	AIModelScopeWorkspace AIModelScope = "workspace"
)

// AIModelUsage 定义业务使用模型的用途，每种用途对应确定的模型类型与输入能力要求。
type AIModelUsage string

const (
	AIModelUsageAgent       AIModelUsage = "agent"
	AIModelUsageSummary     AIModelUsage = "summary"
	AIModelUsageTranslation AIModelUsage = "translation"
	AIModelUsageDecision    AIModelUsage = "decision"
	AIModelUsageEmbedding   AIModelUsage = "embedding"
	AIModelUsageRerank      AIModelUsage = "rerank"
)

// AIModelRequirement 定义用途对模型类型与文本输入能力的要求。
type AIModelRequirement struct {
	Type         AIModelType
	RequiresText bool
}

// Requirement 返回用途要求的模型类型与输入能力；未知用途返回 false。
func (u AIModelUsage) Requirement() (AIModelRequirement, bool) {
	switch u {
	case AIModelUsageAgent, AIModelUsageSummary, AIModelUsageTranslation:
		return AIModelRequirement{Type: AIModelTypeChat, RequiresText: true}, true
	case AIModelUsageDecision:
		return AIModelRequirement{Type: AIModelTypeDecision}, true
	case AIModelUsageEmbedding:
		return AIModelRequirement{Type: AIModelTypeEmbedding}, true
	case AIModelUsageRerank:
		return AIModelRequirement{Type: AIModelTypeRerank}, true
	default:
		return AIModelRequirement{}, false
	}
}

// AIModelInputModality 定义模型支持的输入模态。
type AIModelInputModality string

const (
	AIModelInputModalityText  AIModelInputModality = "text"
	AIModelInputModalityImage AIModelInputModality = "image"
	AIModelInputModalityAudio AIModelInputModality = "audio"
	AIModelInputModalityVideo AIModelInputModality = "video"
)

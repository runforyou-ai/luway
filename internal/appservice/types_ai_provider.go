package appservice

import "github.com/runforyou-ai/luway/internal/domain"

// AIProviderBrand 表示模型服务供应商品牌。
type AIProviderBrand = domain.AIProviderBrand

// AIProviderCredentialType 表示访问模型服务所需的凭据类型。
type AIProviderCredentialType = domain.AIProviderCredentialType

// AIModelType 表示 AI 模型用途。
type AIModelType = domain.AIModelType

// AIModelUsage 表示业务使用模型的用途，每种用途对应确定的模型类型与输入能力要求。
type AIModelUsage = domain.AIModelUsage

// AIModelInputModality 表示模型支持的输入模态。
type AIModelInputModality = domain.AIModelInputModality

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

// AIProviderModel 定义模型服务供应商的模型目录项，保存时编号为空表示新增模型。
type AIProviderModel struct {
	ID              string                 `json:"id"`
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
	ID         string      `json:"id"`
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

// AIModelScope 表示模型范围：平台模型由平台提供，工作区模型由工作区自行配置。
type AIModelScope = domain.AIModelScope

// AIModelOptionProvider 定义工作区模型选项所属的供应商。
type AIModelOptionProvider struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Brand AIProviderBrand `json:"brand"`
}

// AIModelOption 定义模型选择器中的模型；工作区模型带所属供应商，共享模型按来源标识展示。
type AIModelOption struct {
	ID              string                 `json:"id"`
	Scope           AIModelScope           `json:"scope"`
	Name            string                 `json:"name"`
	Type            AIModelType            `json:"type"`
	InputModalities []AIModelInputModality `json:"inputModalities"`
	Provider        *AIModelOptionProvider `json:"provider"`
}

// AIModelOptionList 定义满足某一用途的模型选项。
type AIModelOptionList struct {
	Models []AIModelOption `json:"models"`
}

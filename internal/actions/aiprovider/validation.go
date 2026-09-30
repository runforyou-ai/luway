//go:build server

package aiprovider

import (
	"strings"
	"unicode/utf8"

	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
)

// ValidationCode 标识模型服务供应商字段校验结果。
type ValidationCode = common.FieldCode

const (
	ValidationBrandInvalid          ValidationCode = "AI_PROVIDER_BRAND_INVALID"
	ValidationCredentialTypeInvalid ValidationCode = "AI_PROVIDER_CREDENTIAL_TYPE_INVALID"
	ValidationNameRequired          ValidationCode = "AI_PROVIDER_NAME_REQUIRED"
	ValidationNameTooLong           ValidationCode = "AI_PROVIDER_NAME_TOO_LONG"
	ValidationNameDuplicate         ValidationCode = "AI_PROVIDER_NAME_DUPLICATE"
	ValidationAPIKeyRequired        ValidationCode = "AI_PROVIDER_API_KEY_REQUIRED"
	ValidationAPIKeyTooLong         ValidationCode = "AI_PROVIDER_API_KEY_TOO_LONG"
	ValidationAPIURLRequired        ValidationCode = "AI_PROVIDER_API_URL_REQUIRED"
	ValidationAPIURLInvalid         ValidationCode = "AI_PROVIDER_API_URL_INVALID"
	ValidationModelsInvalid         ValidationCode = "AI_PROVIDER_MODELS_INVALID"
	ValidationModelsInUse           ValidationCode = "AI_PROVIDER_MODELS_IN_USE"
)

const (
	// maxNameLength 是供应商名称的最大字符数。
	maxNameLength = 100
	// maxModelFieldLength 是模型标识和名称的最大字符数。
	maxModelFieldLength = 200
	// maxAPIKeyBytes 是 API 密钥的最大字节数。
	maxAPIKeyBytes = 2048
)

// ValidationError 表示模型服务供应商字段校验失败。
type ValidationError = common.FieldError

// normalizeInput 规范化模型服务供应商输入并校验模型目录。
func normalizeInput(input Input) (Input, map[string]ValidationCode) {
	fields := make(map[string]ValidationCode)
	input.Name = strings.TrimSpace(input.Name)
	connection, connectionFields := normalizeConnectionInput(ConnectionInput{
		Brand: input.Brand, CredentialType: input.CredentialType, APIKey: input.APIKey, APIURL: input.APIURL,
	})
	input.Brand = connection.Brand
	input.CredentialType = connection.CredentialType
	input.APIKey = connection.APIKey
	input.APIURL = connection.APIURL
	for field, code := range connectionFields {
		fields[field] = code
	}

	if input.Name == "" {
		fields["name"] = ValidationNameRequired
	} else if utf8.RuneCountInString(input.Name) > maxNameLength {
		fields["name"] = ValidationNameTooLong
	}

	seen := make(map[string]struct{}, len(input.Models))
	models := make([]Model, 0, len(input.Models))
	for _, model := range input.Models {
		model.Identifier = strings.TrimSpace(model.Identifier)
		model.Name = strings.TrimSpace(model.Name)
		if model.Identifier == "" || utf8.RuneCountInString(model.Identifier) > maxModelFieldLength ||
			model.Name == "" || utf8.RuneCountInString(model.Name) > maxModelFieldLength ||
			model.ContextWindow <= 0 || !normalizeModel(&model) {
			fields["models"] = ValidationModelsInvalid
			continue
		}
		if _, exists := seen[model.Identifier]; exists {
			fields["models"] = ValidationModelsInvalid
			continue
		}
		seen[model.Identifier] = struct{}{}
		models = append(models, model)
	}
	if len(models) == 0 {
		fields["models"] = ValidationModelsInvalid
	}
	input.Models = models
	return input, fields
}

// normalizeConnectionInput 规范化并校验模型服务连接草稿。
func normalizeConnectionInput(input ConnectionInput) (ConnectionInput, map[string]ValidationCode) {
	fields := make(map[string]ValidationCode)
	input.APIKey = strings.TrimSpace(input.APIKey)
	input.APIURL = strings.TrimSpace(input.APIURL)
	if !domain.ValidAIProviderBrand(input.Brand) {
		fields["brand"] = ValidationBrandInvalid
	}
	switch {
	case !domain.ValidAIProviderCredentialType(input.CredentialType):
		fields["credentialType"] = ValidationCredentialTypeInvalid
	case input.CredentialType == domain.AIProviderCredentialTypeNone:
		// 无凭据只适用于自建或本机部署的服务，其余品牌必须配置密钥。
		if !domain.AIProviderBrandSupportsNoCredential(input.Brand) {
			fields["credentialType"] = ValidationCredentialTypeInvalid
		}
		input.APIKey = ""
	case input.APIKey == "":
		fields["apiKey"] = ValidationAPIKeyRequired
	case len(input.APIKey) > maxAPIKeyBytes:
		fields["apiKey"] = ValidationAPIKeyTooLong
	}
	if input.APIURL == "" {
		fields["apiUrl"] = ValidationAPIURLRequired
	} else if !common.ValidHTTPBaseURL(input.APIURL) {
		fields["apiUrl"] = ValidationAPIURLInvalid
	}
	return input, fields
}

// normalizeModel 规范化并校验模型用途与输入模态。
func normalizeModel(model *Model) bool {
	if !validInputModalities(model.InputModalities) {
		return false
	}
	switch model.Type {
	case domain.AIModelTypeChat:
		return model.MaxOutputTokens > 0
	case domain.AIModelTypeEmbedding, domain.AIModelTypeRerank, domain.AIModelTypeDecision:
		model.MaxOutputTokens = 0
		return true
	default:
		return false
	}
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

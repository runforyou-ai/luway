//go:build server

package aiprovider

import (
	"strings"
	"unicode/utf8"

	"github.com/runforyou-ai/luway/internal/actions/aimodel"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
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
	// maxAPIKeyBytes 是 API 密钥的最大字节数。
	maxAPIKeyBytes = 2048
)

// ValidationError 表示模型服务供应商字段校验失败。
type ValidationError = common.FieldError

// normalizeInput 规范化模型服务供应商输入并校验模型目录。
func normalizeInput(input Input) (Input, map[string]ValidationCode) {
	name, connection, fields := normalizeProvider(input.Name, ConnectionInput{
		Brand: input.Brand, CredentialType: input.CredentialType, APIKey: input.APIKey, APIURL: input.APIURL,
	})
	input.Name = name
	input.Brand = connection.Brand
	input.CredentialType = connection.CredentialType
	input.APIKey = connection.APIKey
	input.APIURL = connection.APIURL

	seen := make(map[string]struct{}, len(input.Models))
	seenIDs := make(map[string]struct{}, len(input.Models))
	models := make([]Model, 0, len(input.Models))
	for _, model := range input.Models {
		model.Identifier = strings.TrimSpace(model.Identifier)
		// 已有模型的编号须为不重复的 UUID。
		if model.ID != "" {
			id, valid := common.NormalizeUUID(model.ID)
			if _, duplicated := seenIDs[id]; !valid || duplicated {
				fields["models"] = ValidationModelsInvalid
				continue
			}
			model.ID = id
			seenIDs[id] = struct{}{}
		}
		if !aimodel.ValidIdentifier(model.Identifier) || !normalizeModel(&model) {
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

// normalizeProvider 规范化并校验供应商名称与连接配置。
func normalizeProvider(name string, connection ConnectionInput) (string, ConnectionInput, map[string]ValidationCode) {
	name = strings.TrimSpace(name)
	connection, fields := normalizeConnectionInput(connection)
	if name == "" {
		fields["name"] = ValidationNameRequired
	} else if utf8.RuneCountInString(name) > maxNameLength {
		fields["name"] = ValidationNameTooLong
	}
	return name, connection, fields
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

// normalizeModel 规范化并校验模型属性。
func normalizeModel(model *Model) bool {
	spec := aimodel.Spec{
		Name: model.Name, Type: model.Type, InputModalities: model.InputModalities,
		ContextWindow: model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens,
	}
	if aimodel.NormalizeSpec(&spec) != "" {
		return false
	}
	model.Name, model.MaxOutputTokens = spec.Name, spec.MaxOutputTokens
	return true
}

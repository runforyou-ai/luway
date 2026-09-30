// Package modelprovider 实现模型服务供应商的连接探测与模型发现适配器。
package modelprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
)

// HTTPDoer 定义模型服务探测需要的最小 HTTP 客户端契约。
type HTTPDoer = connectiontest.HTTPDoer

// Config 定义创建模型服务探测器需要的强类型配置。
type Config struct {
	Brand  domain.AIProviderBrand
	APIKey string
	APIURL string
}

// Factory 根据强类型配置创建一个供应商探测器。
type Factory func(Config) (connectiontest.Probe, error)

// Registry 按供应商品牌选择连接探测和模型发现适配器。
type Registry struct {
	factories   map[domain.AIProviderBrand]Factory
	discoverers map[domain.AIProviderBrand]DiscovererFactory
}

// NewRegistry 创建内置供应商品牌注册表。
func NewRegistry(client HTTPDoer) *Registry {
	openAICompatible := newOpenAICompatibleFactory(client)
	return &Registry{factories: map[domain.AIProviderBrand]Factory{
		domain.AIProviderBrandDeepSeek:   openAICompatible,
		domain.AIProviderBrandOpenAI:     openAICompatible,
		domain.AIProviderBrandAnthropic:  newAnthropicFactory(client),
		domain.AIProviderBrandGoogle:     newGoogleFactory(client),
		domain.AIProviderBrandAlibaba:    newAlibabaFactory(client),
		domain.AIProviderBrandMoonshot:   openAICompatible,
		domain.AIProviderBrandZhipu:      openAICompatible,
		domain.AIProviderBrandVolcengine: openAICompatible,
		domain.AIProviderBrandMiniMax:    openAICompatible,
		domain.AIProviderBrandXAI:        openAICompatible,
		domain.AIProviderBrandMistral:    openAICompatible,
		domain.AIProviderBrandOpenRouter: newOpenRouterFactory(client),
		domain.AIProviderBrandTypeSafe:   newTypeSafeFactory(client),

		domain.AIProviderBrandOllama:           newOllamaFactory(client),
		domain.AIProviderBrandOpenAICompatible: openAICompatible,
	}, discoverers: map[domain.AIProviderBrand]DiscovererFactory{
		domain.AIProviderBrandOpenRouter:       newOpenRouterDiscovererFactory(client),
		domain.AIProviderBrandOllama:           newOllamaDiscovererFactory(client),
		domain.AIProviderBrandOpenAICompatible: newOpenAICompatibleDiscovererFactory(client),
	}}
}

// NewProbe 返回指定品牌的连接探测器。
func (r *Registry) NewProbe(config Config) (connectiontest.Probe, error) {
	factory, ok := r.factories[config.Brand]
	if !ok {
		return nil, connectiontest.NewError(
			connectiontest.StageCapability,
			connectiontest.FailureInvalidConfig,
			fmt.Errorf("unsupported model provider brand %q", config.Brand),
		)
	}
	return factory(config)
}

// httpProbe 使用供应商只读接口验证地址、凭据和基本响应契约。
type httpProbe struct {
	client   HTTPDoer
	request  *http.Request
	validate func(io.Reader) error
}

// Run 执行模型服务供应商 HTTP 探测。
func (p *httpProbe) Run(ctx context.Context) error {
	return connectiontest.ReadHTTPResponse(ctx, p.client, p.request, p.validate)
}

// newOpenAICompatibleFactory 创建 OpenAI 兼容模型列表探测器工厂。
func newOpenAICompatibleFactory(client HTTPDoer) Factory {
	return func(config Config) (connectiontest.Probe, error) {
		// 创建 OpenAI 兼容的模型列表请求。
		requestURL, err := connectiontest.AppendPath(config.APIURL, "models")
		if err != nil {
			return nil, connectiontest.InvalidConfigError(err)
		}
		request, err := http.NewRequest(http.MethodGet, requestURL, nil)
		if err != nil {
			return nil, connectiontest.InvalidConfigError(err)
		}
		setHeaders(request, config.APIKey)
		return &httpProbe{client: client, request: request, validate: connectiontest.ValidateDataList}, nil
	}
}

// newOllamaFactory 创建 Ollama 原生模型列表探测器工厂。
func newOllamaFactory(client HTTPDoer) Factory {
	return func(config Config) (connectiontest.Probe, error) {
		requestURL, err := ollamaEndpoint(config.APIURL, "api/tags")
		if err != nil {
			return nil, connectiontest.InvalidConfigError(err)
		}
		request, err := http.NewRequest(http.MethodGet, requestURL, nil)
		if err != nil {
			return nil, connectiontest.InvalidConfigError(err)
		}
		setHeaders(request, config.APIKey)
		return &httpProbe{client: client, request: request, validate: validateModelsArray}, nil
	}
}

// newAlibabaFactory 创建阿里云百炼原生模型列表探测器工厂。
func newAlibabaFactory(client HTTPDoer) Factory {
	return func(config Config) (connectiontest.Probe, error) {
		endpoint, err := alibabaModelsURL(config.APIURL)
		if err != nil {
			return nil, connectiontest.InvalidConfigError(err)
		}
		request, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, connectiontest.InvalidConfigError(err)
		}
		setHeaders(request, config.APIKey)
		return &httpProbe{client: client, request: request, validate: validateAlibabaModelList}, nil
	}
}

// newAnthropicFactory 创建 Anthropic 模型列表探测器工厂，凭据通过 x-api-key 传递。
func newAnthropicFactory(client HTTPDoer) Factory {
	return func(config Config) (connectiontest.Probe, error) {
		requestURL, err := connectiontest.AppendPath(config.APIURL, "v1/models")
		if err != nil {
			return nil, connectiontest.InvalidConfigError(err)
		}
		request, err := http.NewRequest(http.MethodGet, requestURL, nil)
		if err != nil {
			return nil, connectiontest.InvalidConfigError(err)
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("x-api-key", config.APIKey)
		request.Header.Set("anthropic-version", "2023-06-01")
		return &httpProbe{client: client, request: request, validate: connectiontest.ValidateDataList}, nil
	}
}

// newGoogleFactory 创建 Gemini API 模型列表探测器工厂，凭据通过 x-goog-api-key 传递。
func newGoogleFactory(client HTTPDoer) Factory {
	return func(config Config) (connectiontest.Probe, error) {
		requestURL, err := connectiontest.AppendPath(config.APIURL, "v1beta/models")
		if err != nil {
			return nil, connectiontest.InvalidConfigError(err)
		}
		request, err := http.NewRequest(http.MethodGet, requestURL, nil)
		if err != nil {
			return nil, connectiontest.InvalidConfigError(err)
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("x-goog-api-key", config.APIKey)
		return &httpProbe{client: client, request: request, validate: validateModelsArray}, nil
	}
}

// newOpenRouterFactory 创建 OpenRouter 密钥信息探测器工厂，模型列表接口无需凭据，改读密钥信息校验凭据。
func newOpenRouterFactory(client HTTPDoer) Factory {
	return func(config Config) (connectiontest.Probe, error) {
		requestURL, err := connectiontest.AppendPath(config.APIURL, "key")
		if err != nil {
			return nil, connectiontest.InvalidConfigError(err)
		}
		request, err := http.NewRequest(http.MethodGet, requestURL, nil)
		if err != nil {
			return nil, connectiontest.InvalidConfigError(err)
		}
		setHeaders(request, config.APIKey)
		return &httpProbe{client: client, request: request, validate: validateOpenRouterKey}, nil
	}
}

// newTypeSafeFactory 创建 TypeSafe 模型列表探测器工厂。
func newTypeSafeFactory(client HTTPDoer) Factory {
	return func(config Config) (connectiontest.Probe, error) {
		requestURL, err := connectiontest.AppendPath(config.APIURL, "models")
		if err != nil {
			return nil, connectiontest.InvalidConfigError(err)
		}
		request, err := http.NewRequest(http.MethodGet, requestURL, nil)
		if err != nil {
			return nil, connectiontest.InvalidConfigError(err)
		}
		setHeaders(request, config.APIKey)
		return &httpProbe{client: client, request: request, validate: validateModelsArray}, nil
	}
}

// setHeaders 设置模型服务探测的通用请求头，没有凭据的服务不携带鉴权头。
func setHeaders(request *http.Request, apiKey string) {
	request.Header.Set("Accept", "application/json")
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
}

// ollamaEndpoint 把供应商配置地址规范为 Ollama 原生接口地址。
func ollamaEndpoint(baseURL, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("model base URL must include scheme and host")
	}
	parsed.Path = strings.TrimSuffix(strings.TrimSuffix(parsed.Path, "/"), "/v1")
	parsed.RawPath = ""
	return connectiontest.AppendPath(parsed.String(), path)
}

// alibabaModelsURL 把 OpenAI 兼容或原生基础地址转换为百炼模型列表地址。
func alibabaModelsURL(baseURL string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	path := strings.TrimSuffix(parsed.Path, "/")
	switch {
	case strings.HasSuffix(path, "/compatible-mode/v1"):
		path = strings.TrimSuffix(path, "/compatible-mode/v1") + "/api/v1/models"
	case strings.HasSuffix(path, "/api/v1"):
		path += "/models"
	default:
		path += "/api/v1/models"
	}
	parsed.RawPath = ""
	parsed.Path = path
	return parsed.String(), nil
}

// validateModelsArray 校验带 models 数组的模型列表最小响应契约，适用于 Ollama、Gemini API 和 TypeSafe。
func validateModelsArray(reader io.Reader) error {
	var payload struct {
		Models json.RawMessage `json:"models"`
	}
	if err := json.NewDecoder(reader).Decode(&payload); err != nil {
		return err
	}
	if len(payload.Models) == 0 || payload.Models[0] != '[' {
		return errors.New("model list response does not contain a models array")
	}
	return nil
}

// validateAlibabaModelList 校验阿里云百炼模型列表的最小响应契约。
func validateAlibabaModelList(reader io.Reader) error {
	var payload struct {
		Success bool `json:"success"`
		Output  struct {
			Models json.RawMessage `json:"models"`
		} `json:"output"`
	}
	if err := json.NewDecoder(reader).Decode(&payload); err != nil {
		return err
	}
	if !payload.Success || len(payload.Output.Models) == 0 || payload.Output.Models[0] != '[' {
		return errors.New("model list response does not contain a successful models array")
	}
	return nil
}

// validateOpenRouterKey 校验 OpenRouter 密钥信息的最小响应契约。
func validateOpenRouterKey(reader io.Reader) error {
	var payload struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(reader).Decode(&payload); err != nil {
		return err
	}
	if len(payload.Data) == 0 || payload.Data[0] != '{' {
		return errors.New("key response does not contain a data object")
	}
	return nil
}

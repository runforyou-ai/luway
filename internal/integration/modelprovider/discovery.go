package modelprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
)

// DiscoveredModel 定义从模型服务实例读取到的模型目录项，未知项由使用者补全。
type DiscoveredModel struct {
	Identifier      string
	Name            string
	Type            domain.AIModelType
	InputModalities []domain.AIModelInputModality
	ContextWindow   int64
	MaxOutputTokens int64
}

// Discoverer 读取模型服务实例当前可用的模型目录。
type Discoverer interface {
	Discover(ctx context.Context) ([]DiscoveredModel, error)
}

// DiscovererFactory 根据强类型配置创建一个模型发现适配器。
type DiscovererFactory func(Config) (Discoverer, error)

// SupportsDiscovery 判断品牌是否可以从服务实例读取模型目录。
func (r *Registry) SupportsDiscovery(brand domain.AIProviderBrand) bool {
	_, ok := r.discoverers[brand]
	return ok
}

// NewDiscoverer 返回指定品牌的模型发现适配器。
func (r *Registry) NewDiscoverer(config Config) (Discoverer, error) {
	factory, ok := r.discoverers[config.Brand]
	if !ok {
		return nil, connectiontest.NewError(
			connectiontest.StageCapability,
			connectiontest.FailureInvalidConfig,
			fmt.Errorf("model provider brand %q does not support model discovery", config.Brand),
		)
	}
	return factory(config)
}

// newOllamaDiscovererFactory 创建 Ollama 原生接口的模型发现适配器工厂。
func newOllamaDiscovererFactory(client HTTPDoer) DiscovererFactory {
	return func(config Config) (Discoverer, error) {
		return &ollamaDiscoverer{client: client, config: config}, nil
	}
}

// ollamaDetailConcurrency 是同时读取 Ollama 模型详情的请求数上限。
const ollamaDetailConcurrency = 4

// ollamaDiscoverer 通过 Ollama 原生接口读取已安装模型及其能力。
type ollamaDiscoverer struct {
	client HTTPDoer
	config Config
}

// Discover 读取 Ollama 已安装模型，并按模型能力补全用途、输入模态和上下文长度。
func (d *ollamaDiscoverer) Discover(ctx context.Context) ([]DiscoveredModel, error) {
	var payload struct {
		Models []struct {
			Model string `json:"model"`
			Name  string `json:"name"`
		} `json:"models"`
	}
	if err := d.request(ctx, http.MethodGet, "api/tags", nil, &payload); err != nil {
		return nil, err
	}
	models := make([]DiscoveredModel, 0, len(payload.Models))
	for _, item := range payload.Models {
		identifier := strings.TrimSpace(item.Model)
		if identifier == "" {
			identifier = strings.TrimSpace(item.Name)
		}
		if identifier == "" {
			continue
		}
		models = append(models, DiscoveredModel{
			Identifier:      identifier,
			Name:            identifier,
			Type:            domain.AIModelTypeChat,
			InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText},
		})
	}
	// 有限并发读取各模型的能力和上下文设定，结果写回原位置，读取失败时保留列表中的基础信息。
	var group errgroup.Group
	group.SetLimit(ollamaDetailConcurrency)
	for index := range models {
		group.Go(func() error {
			var detail struct {
				Capabilities []string `json:"capabilities"`
				Parameters   string   `json:"parameters"`
			}
			if err := d.request(ctx, http.MethodPost, "api/show", map[string]string{"model": models[index].Identifier}, &detail); err == nil {
				applyOllamaDetail(&models[index], detail.Capabilities, detail.Parameters)
			}
			return nil
		})
	}
	_ = group.Wait()
	return models, nil
}

// request 调用 Ollama 原生接口并解析响应体。
func (d *ollamaDiscoverer) request(ctx context.Context, method, path string, body any, output any) error {
	requestURL, err := ollamaEndpoint(d.config.APIURL, path)
	if err != nil {
		return connectiontest.InvalidConfigError(err)
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(encoded))
	}
	request, err := http.NewRequest(method, requestURL, reader)
	if err != nil {
		return connectiontest.InvalidConfigError(err)
	}
	setHeaders(request, d.config.APIKey)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return connectiontest.ReadHTTPResponse(ctx, d.client, request, func(response io.Reader) error {
		return json.NewDecoder(response).Decode(output)
	})
}

// applyOllamaDetail 按 Ollama 模型能力和 Modelfile 参数补全模型用途、输入模态与长度限制。
func applyOllamaDetail(model *DiscoveredModel, capabilities []string, parameters string) {
	for _, capability := range capabilities {
		switch capability {
		case "embedding":
			model.Type = domain.AIModelTypeEmbedding
		case "vision":
			model.InputModalities = []domain.AIModelInputModality{
				domain.AIModelInputModalityText,
				domain.AIModelInputModalityImage,
			}
		}
	}
	// 上下文窗口取 Modelfile 中的 num_ctx。模型元信息中的 context_length 是模型支持的上限，
	// 与 Ollama 实际加载的窗口无关；未设定 num_ctx 时留空，由用户按部署实际情况填写。
	for line := range strings.SplitSeq(parameters, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "num_ctx" {
			continue
		}
		if length, err := strconv.ParseInt(fields[1], 10, 64); err == nil && length > 0 {
			model.ContextWindow = length
		}
		break
	}
	if model.Type == domain.AIModelTypeChat {
		// 本地模型的输出长度只受上下文限制，以上下文窗口作为可编辑的初始值。
		model.MaxOutputTokens = model.ContextWindow
	}
}

// newOpenAICompatibleDiscovererFactory 创建 OpenAI 兼容模型列表的发现适配器工厂。
func newOpenAICompatibleDiscovererFactory(client HTTPDoer) DiscovererFactory {
	return func(config Config) (Discoverer, error) {
		return &openAICompatibleDiscoverer{client: client, config: config}, nil
	}
}

// openAICompatibleDiscoverer 通过 OpenAI 兼容模型列表接口读取模型标识。
type openAICompatibleDiscoverer struct {
	client HTTPDoer
	config Config
}

// Discover 读取 OpenAI 兼容模型列表，接口只提供标识，其余字段由使用者补全。
func (d *openAICompatibleDiscoverer) Discover(ctx context.Context) ([]DiscoveredModel, error) {
	requestURL, err := connectiontest.AppendPath(d.config.APIURL, "models")
	if err != nil {
		return nil, connectiontest.InvalidConfigError(err)
	}
	request, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, connectiontest.InvalidConfigError(err)
	}
	setHeaders(request, d.config.APIKey)
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := connectiontest.ReadHTTPResponse(ctx, d.client, request, func(response io.Reader) error {
		if err := json.NewDecoder(response).Decode(&payload); err != nil {
			return err
		}
		if payload.Data == nil {
			return errors.New("model list response does not contain a data array")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	models := make([]DiscoveredModel, 0, len(payload.Data))
	for _, item := range payload.Data {
		identifier := strings.TrimSpace(item.ID)
		if identifier == "" {
			continue
		}
		models = append(models, DiscoveredModel{
			Identifier:      identifier,
			Name:            identifier,
			Type:            domain.AIModelTypeChat,
			InputModalities: []domain.AIModelInputModality{domain.AIModelInputModalityText},
		})
	}
	return models, nil
}

// newOpenRouterDiscovererFactory 创建 OpenRouter 模型列表的发现适配器工厂。
func newOpenRouterDiscovererFactory(client HTTPDoer) DiscovererFactory {
	return func(config Config) (Discoverer, error) {
		return &openRouterDiscoverer{client: client, config: config}, nil
	}
}

// openRouterDiscoverer 通过 OpenRouter 模型列表读取全部输出类型的模型及其能力。
type openRouterDiscoverer struct {
	client HTTPDoer
	config Config
}

// Discover 读取 OpenRouter 模型列表，按输出类型识别对话、向量、重排和判断模型，其余输出类型的模型不列出。
func (d *openRouterDiscoverer) Discover(ctx context.Context) ([]DiscoveredModel, error) {
	requestURL, err := connectiontest.AppendPath(d.config.APIURL, "models")
	if err != nil {
		return nil, connectiontest.InvalidConfigError(err)
	}
	request, err := http.NewRequest(http.MethodGet, requestURL+"?output_modalities=all", nil)
	if err != nil {
		return nil, connectiontest.InvalidConfigError(err)
	}
	setHeaders(request, d.config.APIKey)
	var payload struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			ContextLength int64  `json:"context_length"`
			Architecture  struct {
				InputModalities  []string `json:"input_modalities"`
				OutputModalities []string `json:"output_modalities"`
			} `json:"architecture"`
			TopProvider struct {
				MaxCompletionTokens int64 `json:"max_completion_tokens"`
			} `json:"top_provider"`
		} `json:"data"`
	}
	if err := connectiontest.ReadHTTPResponse(ctx, d.client, request, func(response io.Reader) error {
		if err := json.NewDecoder(response).Decode(&payload); err != nil {
			return err
		}
		if payload.Data == nil {
			return errors.New("model list response does not contain a data array")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	models := make([]DiscoveredModel, 0, len(payload.Data))
	for _, item := range payload.Data {
		identifier := strings.TrimSpace(item.ID)
		if identifier == "" || len(item.Architecture.OutputModalities) != 1 {
			continue
		}
		model := DiscoveredModel{Identifier: identifier, Name: strings.TrimSpace(item.Name), ContextWindow: item.ContextLength}
		if model.Name == "" {
			model.Name = identifier
		}
		switch item.Architecture.OutputModalities[0] {
		case "text":
			model.Type = domain.AIModelTypeChat
			model.MaxOutputTokens = item.TopProvider.MaxCompletionTokens
		case "embeddings":
			model.Type = domain.AIModelTypeEmbedding
		case "rerank":
			model.Type = domain.AIModelTypeRerank
		case "decisions":
			model.Type = domain.AIModelTypeDecision
		default:
			continue
		}
		// 只保留系统支持的输入模态，文件等其他输入不列出。
		for _, modality := range item.Architecture.InputModalities {
			switch value := domain.AIModelInputModality(modality); value {
			case domain.AIModelInputModalityText, domain.AIModelInputModalityImage,
				domain.AIModelInputModalityAudio, domain.AIModelInputModalityVideo:
				if !slices.Contains(model.InputModalities, value) {
					model.InputModalities = append(model.InputModalities, value)
				}
			}
		}
		if !slices.Contains(model.InputModalities, domain.AIModelInputModalityText) {
			continue
		}
		models = append(models, model)
	}
	return models, nil
}

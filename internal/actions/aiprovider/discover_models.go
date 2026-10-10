//go:build server

package aiprovider

import (
	"context"
	"time"

	"github.com/runforyou-ai/einorun/llm"
	"github.com/runforyou-ai/einorun/provider/discovery"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/runforyou-ai/support/arr"
)

// discoveryTimeout 是一次模型发现的总时限，覆盖逐个读取模型详情。
const discoveryTimeout = 30 * time.Second

// DiscoverModelsAction 读取模型服务实例当前可用的模型目录。
type DiscoverModelsAction struct {
	runner *connectiontest.Runner
	client connectiontest.HTTPDoer
}

// NewDiscoverModelsAction 创建模型发现操作。
func NewDiscoverModelsAction(client connectiontest.HTTPDoer) *DiscoverModelsAction {
	return &DiscoverModelsAction{runner: connectiontest.NewRunner(discoveryTimeout), client: client}
}

// Execute 校验草稿配置并读取服务实例提供的模型，服务未提供的字段保持零值由调用方补全。
func (a *DiscoverModelsAction) Execute(ctx context.Context, input ConnectionInput) ([]Model, error) {
	input, fields := normalizeConnectionInput(input)
	// 模型目录固定的品牌只提供预设清单，按品牌字段拒绝。
	if len(fields) == 0 && !supportsDiscovery(input.Brand) {
		fields = map[string]ValidationCode{"brand": ValidationBrandInvalid}
	}
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	models := make([]Model, 0)
	err := a.runner.Run(ctx, connectionTarget(input.Brand), connectiontest.ProbeFunc(func(ctx context.Context) error {
		discovered, err := discovery.Discover(ctx, a.client, discovery.Endpoint{
			Brand: vendorBrand(input.Brand), BaseURL: input.APIURL, APIKey: input.APIKey,
		})
		if err != nil {
			return connectionError(err)
		}
		for _, item := range discovered {
			// 发现结果只列文本以外的输入方式，模型目录中的输入方式包含文本。
			modalities := append([]domain.AIModelInputModality{domain.AIModelInputModalityText},
				arr.Map(item.Inputs, func(m llm.Modality) domain.AIModelInputModality { return domain.AIModelInputModality(m) })...)
			models = append(models, Model{
				Identifier: item.ID, Name: item.Name, Type: domain.AIModelType(item.Type), InputModalities: modalities,
				ContextWindow: item.ContextWindow, MaxOutputTokens: item.MaxOutputTokens,
			})
		}
		return nil
	}))
	if err != nil {
		return nil, err
	}
	return models, nil
}

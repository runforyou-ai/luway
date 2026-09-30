//go:build server

package aiprovider

import (
	"context"
	"time"

	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/modelprovider"
	"github.com/runforyou-ai/cervi/pkg/connectiontest"
)

// discoveryTimeout 是一次模型发现的总时限，按模型逐个读取详情比单次连接探测慢。
const discoveryTimeout = 30 * time.Second

// DiscoverModelsAction 读取模型服务实例当前可用的模型目录。
type DiscoverModelsAction struct {
	runner   *connectiontest.Runner
	registry *modelprovider.Registry
}

// NewDiscoverModelsAction 创建模型发现操作。
func NewDiscoverModelsAction(registry *modelprovider.Registry) *DiscoverModelsAction {
	return &DiscoverModelsAction{runner: connectiontest.NewRunner(discoveryTimeout), registry: registry}
}

// Execute 校验草稿配置并读取服务实例提供的模型，服务未提供的字段保持零值由调用方补全。
func (a *DiscoverModelsAction) Execute(ctx context.Context, input ConnectionInput) ([]Model, error) {
	input, fields := normalizeConnectionInput(input)
	// 模型目录固定的品牌只提供预设清单，按品牌字段拒绝。
	if len(fields) == 0 && !a.registry.SupportsDiscovery(input.Brand) {
		fields = map[string]ValidationCode{"brand": ValidationBrandInvalid}
	}
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	discoverer, err := a.registry.NewDiscoverer(modelprovider.Config{
		Brand: input.Brand, APIKey: input.APIKey, APIURL: input.APIURL,
	})
	if err != nil {
		return nil, err
	}
	models := make([]Model, 0)
	err = a.runner.Run(ctx, connectiontest.Target{
		Category: string(domain.ConnectionProbeModelProvider),
		Adapter:  string(input.Brand),
		Location: string(domain.ConnectionProbeServer),
	}, connectiontest.ProbeFunc(func(ctx context.Context) error {
		discovered, err := discoverer.Discover(ctx)
		if err != nil {
			return err
		}
		for _, item := range discovered {
			models = append(models, Model{
				Identifier: item.Identifier, Name: item.Name, Type: item.Type,
				InputModalities: item.InputModalities,
				ContextWindow:   item.ContextWindow, MaxOutputTokens: item.MaxOutputTokens,
			})
		}
		return nil
	}))
	if err != nil {
		return nil, err
	}
	return models, nil
}

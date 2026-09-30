//go:build server

package aiprovider

import (
	"context"

	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/modelprovider"
	"github.com/runforyou-ai/cervi/pkg/connectiontest"
)

// TestConnectionAction 测试模型服务供应商草稿配置。
type TestConnectionAction struct {
	runner   *connectiontest.Runner
	registry *modelprovider.Registry
}

// NewTestConnectionAction 创建模型服务供应商连接测试操作。
func NewTestConnectionAction(runner *connectiontest.Runner, registry *modelprovider.Registry) *TestConnectionAction {
	return &TestConnectionAction{runner: runner, registry: registry}
}

// Execute 校验草稿配置并执行指定供应商的只读探测。
func (a *TestConnectionAction) Execute(ctx context.Context, input ConnectionInput) error {
	input, fields := normalizeConnectionInput(input)
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	probe, err := a.registry.NewProbe(modelprovider.Config{
		Brand: input.Brand, APIKey: input.APIKey, APIURL: input.APIURL,
	})
	if err != nil {
		return err
	}
	return a.runner.Run(ctx, connectiontest.Target{
		Category: string(domain.ConnectionProbeModelProvider),
		Adapter:  string(input.Brand),
		Location: string(domain.ConnectionProbeServer),
	}, probe)
}

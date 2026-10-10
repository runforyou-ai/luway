//go:build server

package aiprovider

import (
	"context"

	"github.com/runforyou-ai/einorun/provider/apierr"
	"github.com/runforyou-ai/einorun/provider/probe"
	"github.com/runforyou-ai/einorun/provider/vendor"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
)

// TestConnectionAction 测试模型服务供应商草稿配置。
type TestConnectionAction struct {
	runner *connectiontest.Runner
	client connectiontest.HTTPDoer
}

// NewTestConnectionAction 创建模型服务供应商连接测试操作。
func NewTestConnectionAction(runner *connectiontest.Runner, client connectiontest.HTTPDoer) *TestConnectionAction {
	return &TestConnectionAction{runner: runner, client: client}
}

// Execute 校验草稿配置并执行指定供应商的只读探测。
func (a *TestConnectionAction) Execute(ctx context.Context, input ConnectionInput) error {
	input, fields := normalizeConnectionInput(input)
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return a.runner.Run(ctx, connectionTarget(input.Brand), connectiontest.ProbeFunc(func(ctx context.Context) error {
		return connectionError(probe.Run(ctx, a.client, probe.Endpoint{
			Brand: vendorBrand(input.Brand), BaseURL: input.APIURL, APIKey: input.APIKey,
		}))
	}))
}

// connectionTarget 返回模型服务供应商连接测试的日志目标。
func connectionTarget(brand domain.AIProviderBrand) connectiontest.Target {
	return connectiontest.Target{
		Category: string(domain.ConnectionProbeModelProvider),
		Adapter:  string(brand),
		Location: string(domain.ConnectionProbeServer),
	}
}

// vendorBrand 返回供应商品牌在模型服务库中的品牌，两者取值一致。
func vendorBrand(brand domain.AIProviderBrand) vendor.Brand {
	return vendor.Brand(brand)
}

// supportsDiscovery 返回品牌的模型目录是否由服务实例提供。
func supportsDiscovery(brand domain.AIProviderBrand) bool {
	preset, known := vendor.Of(vendorBrand(brand))
	return known && preset.Discovery
}

// connectionError 把模型服务库分类的失败转为连接测试错误，阶段与失败类型取值一致。
func connectionError(err error) error {
	failure, ok := apierr.As(err)
	if !ok {
		return err
	}
	return connectiontest.NewError(connectiontest.Stage(failure.Stage), connectiontest.FailureKind(failure.Kind), failure)
}

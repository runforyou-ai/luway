//go:build server

package businesssystem

import (
	"context"

	"github.com/runforyou-ai/luway/internal/domain"
)

// TestConnectionAction 测试业务系统草稿配置的工具发现能力。
type TestConnectionAction struct{ connector *Connector }

// NewTestConnectionAction 创建业务系统连接测试操作。
func NewTestConnectionAction(connector *Connector) *TestConnectionAction {
	return &TestConnectionAction{connector: connector}
}

// Execute 校验配置并执行只读连接测试，返回归一化后的连接配置与完整工具目录；HTTP 业务系统未填写接口根地址时取文档声明的服务地址。
func (a *TestConnectionAction) Execute(ctx context.Context, input ConnectionInput) (ConnectionInput, []domain.BusinessTool, error) {
	input, fields := normalizeConnectionInput(input)
	if len(fields) > 0 {
		return ConnectionInput{}, nil, &ValidationError{Fields: fields}
	}
	discovery, err := a.connector.Discover(ctx, input)
	if err != nil {
		return ConnectionInput{}, nil, err
	}
	input.Connection = discovery.Connection
	return input, discovery.Tools, nil
}

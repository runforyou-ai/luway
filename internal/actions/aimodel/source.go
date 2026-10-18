//go:build server

package aimodel

import (
	"context"

	"github.com/runforyou-ai/luway/internal/domain"
)

type sourceKey struct{}

// processSource 是本进程默认模型来源，由服务端装配按程序组成设置；集成测试不改此值，改走 WithSource。
var processSource = domain.AIModelSourceWorkspace

// SetProcessSource 设置本进程默认模型来源；空值视为工作区自配。
func SetProcessSource(source domain.AIModelSource) {
	if source == "" {
		source = domain.AIModelSourceWorkspace
	}
	processSource = source
}

// WithSource 把模型来源写入 context，供单次请求或测试覆盖进程默认值。
func WithSource(ctx context.Context, source domain.AIModelSource) context.Context {
	if source == "" {
		source = domain.AIModelSourceWorkspace
	}
	return context.WithValue(ctx, sourceKey{}, source)
}

// SourceOf 返回 context 中的模型来源；未设置时使用进程默认值。
func SourceOf(ctx context.Context) domain.AIModelSource {
	if source, ok := ctx.Value(sourceKey{}).(domain.AIModelSource); ok && source != "" {
		return source
	}
	return processSource
}

// AllowsWorkspaceProviders 表示当前来源是否允许工作区自配模型服务。
func AllowsWorkspaceProviders(ctx context.Context) bool {
	return SourceOf(ctx) == domain.AIModelSourceWorkspace
}

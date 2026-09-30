package connectiontest

import (
	"context"
	"testing"
	"time"
)

// TestRunnerPreservesClassifiedFailure 验证执行器保留适配器给出的稳定错误分类。
func TestRunnerPreservesClassifiedFailure(t *testing.T) {
	runner := NewRunner(time.Second)
	err := runner.Run(context.Background(), Target{
		Category: "model_provider", Adapter: "openai", Location: "server",
	}, ProbeFunc(func(context.Context) error {
		return HTTPStatusError(401)
	}))
	stage, kind, ok := Details(err)
	if !ok || stage != StageAuthenticate || kind != FailureUnauthorized {
		t.Fatalf("stage = %q, kind = %q, ok = %v", stage, kind, ok)
	}
}

// TestRunnerOwnsTimeout 验证所有适配器共用执行器设置的超时。
func TestRunnerOwnsTimeout(t *testing.T) {
	runner := NewRunner(20 * time.Millisecond)
	err := runner.Run(context.Background(), Target{
		Category: "model_provider", Adapter: "http", Location: "server",
	}, ProbeFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	stage, kind, ok := Details(err)
	if !ok || stage != StageConnect || kind != FailureTimeout {
		t.Fatalf("stage = %q, kind = %q, ok = %v", stage, kind, ok)
	}
}

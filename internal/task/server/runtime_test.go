//go:build server

package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestRegisterJSONMarksInvalidPayloadPermanent 验证损坏的任务输入被标记为永久失败。
func TestRegisterJSONMarksInvalidPayloadPermanent(t *testing.T) {
	type input struct {
		Value string `json:"value"`
	}
	registry := NewRegistry()
	if err := registry.RegisterJSON("test.action", func(context.Context, input) error { return nil }); err != nil {
		t.Fatal(err)
	}
	handler, exists := registry.lookup("test.action")
	if !exists {
		t.Fatal("registered handler not found")
	}
	err := handler(context.Background(), []byte(`{"value":`))
	if !IsPermanent(err) {
		t.Fatalf("invalid payload error = %v, want permanent", err)
	}
}

// TestRegisterJSONWithTerminalFailure 解码并执行最终失败业务收尾。
func TestRegisterJSONWithTerminalFailure(t *testing.T) {
	type input struct {
		Value string `json:"value"`
	}
	registry := NewRegistry()
	var finalized string
	terminalErr := errors.New("attempts exhausted")
	if err := registry.RegisterJSONWithTerminalFailure(
		"test.finalized_action",
		func(context.Context, input) error { return nil },
		func(_ context.Context, value input, runErr error) error {
			if !errors.Is(runErr, terminalErr) {
				t.Fatalf("terminal error = %v", runErr)
			}
			finalized = value.Value
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	finalize, exists := registry.lookupTerminalFailure("test.finalized_action")
	if !exists {
		t.Fatal("terminal failure handler not found")
	}
	if err := finalize(context.Background(), json.RawMessage(`{"value":"done"}`), terminalErr); err != nil {
		t.Fatal(err)
	}
	if finalized != "done" {
		t.Fatalf("finalized input = %q", finalized)
	}
	if err := finalize(context.Background(), json.RawMessage(`{"value":`), terminalErr); err != nil {
		t.Fatalf("malformed terminal payload should not block task finalization: %v", err)
	}
}

// TestExecuteHandlerRecoversPanic 验证 Worker 捕获单个 Action 的 panic 并继续运行。
func TestExecuteHandlerRecoversPanic(t *testing.T) {
	err := executeHandler(context.Background(), func(context.Context, json.RawMessage) error {
		panic("broken action")
	}, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "broken action") {
		t.Fatalf("panic error = %v", err)
	}
}

// TestResolveExecutionErrorPreservesHandlerResult 验证 Action 完成后保留其执行结果。
func TestResolveExecutionErrorPreservesHandlerResult(t *testing.T) {
	heartbeatErr := errors.New("lease lost")
	if err := resolveExecutionError(nil, heartbeatErr); err != nil {
		t.Fatalf("successful handler result was replaced: %v", err)
	}
	permanentErr := Permanent(errors.New("invalid input"))
	if err := resolveExecutionError(permanentErr, heartbeatErr); !errors.Is(err, permanentErr) {
		t.Fatalf("permanent handler result was replaced: %v", err)
	}
	permanentCancel := Permanent(context.Canceled)
	if err := resolveExecutionError(permanentCancel, heartbeatErr); !errors.Is(err, permanentCancel) {
		t.Fatalf("permanent cancellation was replaced: %v", err)
	}
	if err := resolveExecutionError(context.Canceled, heartbeatErr); !errors.Is(err, heartbeatErr) {
		t.Fatalf("cancelled handler result = %v, want heartbeat error", err)
	}
}

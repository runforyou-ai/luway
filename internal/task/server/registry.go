//go:build server

package server

import (
	"context"
	"encoding/json"
	"fmt"
)

// Registry 保存服务端可异步执行的 Action。
type Registry struct {
	actions map[string]registeredAction
}

type terminalFailureHandler func(context.Context, json.RawMessage, error) error

type registeredAction struct {
	handler         Handler
	terminalFailure terminalFailureHandler
}

// NewRegistry 创建 Action 注册表。
func NewRegistry() *Registry {
	return &Registry{actions: make(map[string]registeredAction)}
}

// Register 注册一个原始 JSON Action 处理器。
func (r *Registry) Register(name string, handler Handler) error {
	return r.register(name, registeredAction{handler: handler})
}

// register 注册包含可选终态回调的 Action。
func (r *Registry) register(name string, action registeredAction) error {
	if !actionNamePattern.MatchString(name) {
		return fmt.Errorf("invalid task action name %q", name)
	}
	if action.handler == nil {
		return fmt.Errorf("task action %q has nil handler", name)
	}
	if _, exists := r.actions[name]; exists {
		return fmt.Errorf("task action %q already registered", name)
	}
	r.actions[name] = action
	return nil
}

// lookup 查找已注册的 Action 处理器。
func (r *Registry) lookup(name string) (Handler, bool) {
	action, exists := r.actions[name]
	return action.handler, exists
}

// lookupTerminalFailure 查找任务最终失败时的业务收尾。
func (r *Registry) lookupTerminalFailure(name string) (terminalFailureHandler, bool) {
	action, exists := r.actions[name]
	return action.terminalFailure, exists && action.terminalFailure != nil
}

// RegisterJSON 注册使用强类型 JSON 输入的 Action。
func (r *Registry) RegisterJSON[T any](name string, execute func(context.Context, T) error) error {
	return r.Register(name, jsonHandler(name, execute))
}

// jsonHandler 构造先解码强类型 JSON 输入再执行的处理器，解码失败按永久失败处理。
func jsonHandler[T any](name string, execute func(context.Context, T) error) Handler {
	return func(ctx context.Context, payload json.RawMessage) error {
		var input T
		if err := json.Unmarshal(payload, &input); err != nil {
			return Permanent(fmt.Errorf("decode %s payload: %w", name, err))
		}
		return execute(ctx, input)
	}
}

// RegisterJSONWithTerminalFailure 注册强类型 Action 及任务耗尽后的业务收尾。
func (r *Registry) RegisterJSONWithTerminalFailure[T any](name string, execute func(context.Context, T) error, finalize func(context.Context, T, error) error) error {
	if finalize == nil {
		return fmt.Errorf("task action %q has nil terminal failure handler", name)
	}
	action := registeredAction{
		handler: jsonHandler(name, execute),
		terminalFailure: func(ctx context.Context, payload json.RawMessage, runErr error) error {
			var input T
			if err := json.Unmarshal(payload, &input); err != nil {
				return nil
			}
			return finalize(ctx, input, runErr)
		},
	}
	return r.register(name, action)
}

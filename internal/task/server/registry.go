//go:build server

package server

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"

	"github.com/runforyou-ai/jetq"
)

// actionNamePattern 是 Action 名称与计划标识的格式。
var actionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)

// Registry 保存服务端可异步执行的 Action。
type Registry struct {
	actions map[string]registeredAction
}

// registeredAction 把已校验的 Action 装配到任务 Worker。
type registeredAction func(*jetq.Worker)

// NewRegistry 创建 Action 注册表。
func NewRegistry() *Registry {
	return &Registry{actions: make(map[string]registeredAction)}
}

// register 注册包含可选终态回调的 Action。
func (r *Registry) register(name string, action registeredAction) error {
	if !actionNamePattern.MatchString(name) {
		return fmt.Errorf("invalid task action name %q", name)
	}
	if action == nil {
		return fmt.Errorf("task action %q has nil handler", name)
	}
	if _, exists := r.actions[name]; exists {
		return fmt.Errorf("task action %q already registered", name)
	}
	r.actions[name] = action
	return nil
}

// registered 判断 Action 是否已注册。
func (r *Registry) registered(name string) bool {
	_, exists := r.actions[name]
	return exists
}

// RegisterJSON 注册使用强类型 JSON 输入的 Action。
func (r *Registry) RegisterJSON[T any](name string, execute func(context.Context, T) error) error {
	return r.Add(JSONAction(name, execute))
}

// RegisterJSONWithTerminalFailure 注册强类型 Action 及任务最终失败后的业务收尾。
func (r *Registry) RegisterJSONWithTerminalFailure[T any](name string, execute func(context.Context, T) error, finalize func(context.Context, T, error) error) error {
	if execute == nil {
		return fmt.Errorf("task action %q has nil handler", name)
	}
	if finalize == nil {
		return fmt.Errorf("task action %q has nil terminal failure handler", name)
	}
	return r.register(name, func(worker *jetq.Worker) {
		jetq.HandleNamed(worker, name, execute, jetq.OnFailure(func(ctx context.Context, input T, runErr error) {
			if err := finalize(ctx, input, runErr); err != nil {
				slog.ErrorContext(ctx, "异步任务失败收尾出错", "error", err)
			}
		}))
	})
}

// Action 是声明式登记的 Action：名称与处理器，由 Registry.Add 注册。
type Action struct {
	name   string
	action registeredAction
}

// JSONAction 声明使用强类型 JSON 输入的 Action。
func JSONAction[T any](name string, execute func(context.Context, T) error) Action {
	if execute == nil {
		return Action{name: name}
	}
	return Action{name: name, action: func(worker *jetq.Worker) { jetq.HandleNamed(worker, name, execute) }}
}

// Name 返回 Action 名称。
func (a Action) Name() string {
	return a.name
}

// Add 依次注册声明的 Action，名称无效、处理器为空或重复注册时返回错误。
func (r *Registry) Add(actions ...Action) error {
	for _, item := range actions {
		if err := r.register(item.name, item.action); err != nil {
			return err
		}
	}
	return nil
}

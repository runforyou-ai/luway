//go:build server

package servertest

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"uuid"

	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/stretchr/testify/require"
)

// Task 是测试任务登记器记录的一次任务投递。
type Task struct {
	ID      string
	Action  string
	Payload json.RawMessage
	Options servertask.EnqueueOptions
}

// Tasks 记录投递的任务而不执行：事务内投递在事务提交后记录、回滚时丢弃；同一 Action 与去重键已有未取走的任务时不重复记录。
type Tasks struct {
	mu    sync.Mutex
	tasks []Task
}

var _ servertask.DualEnqueuer = (*Tasks)(nil)

// NewTasks 创建测试任务登记器。
func NewTasks() *Tasks {
	return &Tasks{}
}

// Enqueue 立即记录任务。
func (t *Tasks) Enqueue(_ context.Context, actionName string, payload any, options servertask.EnqueueOptions) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	t.record(Task{ID: uuid.NewV7().String(), Action: actionName, Payload: encoded, Options: options})
	return nil
}

// EnqueueIn 在当前写事务提交后记录任务并返回任务编号。
func (t *Tasks) EnqueueIn(ctx context.Context, actionName string, payload any, options servertask.EnqueueOptions) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	task := Task{ID: uuid.NewV7().String(), Action: actionName, Payload: encoded, Options: options}
	serverstorage.AfterCommit(ctx, func(context.Context) error {
		t.record(task)
		return nil
	})
	return task.ID, nil
}

// EnqueueManyIn 在当前写事务提交后按顺序记录一批任务。
func (t *Tasks) EnqueueManyIn(ctx context.Context, requests []servertask.EnqueueRequest) error {
	for _, request := range requests {
		if _, err := t.EnqueueIn(ctx, request.ActionName, request.Payload, request.Options); err != nil {
			return err
		}
	}
	return nil
}

// record 记录一次任务，同一 Action 与去重键已有未取走的任务时跳过。
func (t *Tasks) record(task Task) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if key := task.Options.IdempotencyKey; key != "" {
		for _, existing := range t.tasks {
			if existing.Action == task.Action && existing.Options.IdempotencyKey == key {
				return
			}
		}
	}
	t.tasks = append(t.tasks, task)
}

// Queued 按投递顺序返回指定 Action 未取走的任务，workspaceID 非空时只返回该工作区的任务。
func (t *Tasks) Queued(actionName, workspaceID string) []Task {
	t.mu.Lock()
	defer t.mu.Unlock()
	matched := make([]Task, 0)
	for _, task := range t.tasks {
		if task.Action == actionName && (workspaceID == "" || task.Options.WorkspaceID == workspaceID) {
			matched = append(matched, task)
		}
	}
	return matched
}

// Take 取走并按投递顺序返回指定 Action 的任务，workspaceID 非空时只取该工作区的任务。
func (t *Tasks) Take(actionName, workspaceID string) []Task {
	t.mu.Lock()
	defer t.mu.Unlock()
	taken := make([]Task, 0)
	kept := t.tasks[:0]
	for _, task := range t.tasks {
		if task.Action == actionName && (workspaceID == "" || task.Options.WorkspaceID == workspaceID) {
			taken = append(taken, task)
			continue
		}
		kept = append(kept, task)
	}
	t.tasks = kept
	return taken
}

// Keyed 按投递顺序返回指定 Action 未取走且去重键为 key 的任务。
func (t *Tasks) Keyed(actionName, key string) []Task {
	t.mu.Lock()
	defer t.mu.Unlock()
	matched := make([]Task, 0)
	for _, task := range t.tasks {
		if task.Action == actionName && task.Options.IdempotencyKey == key {
			matched = append(matched, task)
		}
	}
	return matched
}

// takeMatching 取走并按投递顺序返回指定 Action 中满足 match 的任务。
func (t *Tasks) takeMatching(actionName string, match func(Task) bool) []Task {
	t.mu.Lock()
	defer t.mu.Unlock()
	taken := make([]Task, 0)
	kept := t.tasks[:0]
	for _, task := range t.tasks {
		if task.Action == actionName && match(task) {
			taken = append(taken, task)
			continue
		}
		kept = append(kept, task)
	}
	t.tasks = kept
	return taken
}

// QueuedInputs 按投递顺序返回指定 Action 未取走的任务中输入满足 match 的输入。
func QueuedInputs[T any](t *testing.T, tasks *Tasks, actionName string, match func(T) bool) []T {
	t.Helper()
	inputs := make([]T, 0)
	for _, task := range tasks.Queued(actionName, "") {
		if input := TaskPayload[T](t, task); match(input) {
			inputs = append(inputs, input)
		}
	}
	return inputs
}

// TakeInputs 取走指定 Action 中输入满足 match 的任务，按投递顺序返回其输入。
func TakeInputs[T any](t *testing.T, tasks *Tasks, actionName string, match func(T) bool) []T {
	t.Helper()
	taken := tasks.takeMatching(actionName, func(task Task) bool {
		var input T
		return json.Unmarshal(task.Payload, &input) == nil && match(input)
	})
	inputs := make([]T, 0, len(taken))
	for _, task := range taken {
		inputs = append(inputs, TaskPayload[T](t, task))
	}
	return inputs
}

// LatestInput 返回各登记器中指定 Action 未取走且输入满足 match 的任务里最近一次投递的输入。
func LatestInput[T any](t *testing.T, actionName string, match func(T) bool, recorders ...*Tasks) T {
	t.Helper()
	var latest *Task
	var input T
	for _, recorder := range recorders {
		for _, task := range recorder.Queued(actionName, "") {
			// 任务编号是按时间递增的 UUIDv7，比较编号即比较投递先后。
			if candidate := TaskPayload[T](t, task); match(candidate) && (latest == nil || task.ID > latest.ID) {
				latest, input = &task, candidate
			}
		}
	}
	require.NotNil(t, latest, "no queued %s task", actionName)
	return input
}

// TaskPayload 把任务输入解码为 T。
func TaskPayload[T any](t *testing.T, task Task) T {
	t.Helper()
	var input T
	require.NoError(t, json.Unmarshal(task.Payload, &input))
	return input
}

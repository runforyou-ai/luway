//go:build server

package tooldecision

import (
	"errors"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/businesssystem"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

var (
	// ErrToolCallUnavailable 表示工具调用不存在或当前成员不是它的处理成员。
	ErrToolCallUnavailable = errors.New("agent tool call is unavailable")
	// ErrToolCallDecided 表示工具调用已经处理、已过期或当前状态不接受该操作。
	ErrToolCallDecided = errors.New("agent tool call has been decided")
)

// toolCallUnavailableMessage 是批准后业务系统、电脑、工具或授权已不允许执行时记录的失败原因。
const toolCallUnavailableMessage = "业务系统、电脑、工具或授权已变化，操作没有执行。"

// pausedCancelledResult 是暂停等待确认的调用被取消后交给模型的结果。
const pausedCancelledResult = "操作已取消，没有执行：确认人已无法处理这项操作。需要时向当前对方重新确认后再发起。"

// toolCallExecuteTimeout 是批准后执行一次业务系统调用的最长时间。
const toolCallExecuteTimeout = 2 * time.Minute

// Action 处理需要确认或审批的工具调用：成员与客户裁决、核对、批准后执行、过期、取消后的结果事件、本机 Agent 的权限请求，以及派发到电脑的调用有结果后的唤醒。
type Action struct {
	db        *bun.DB
	enqueuer  servertask.TxEnqueuer
	scopes    RunScopes
	connector *businesssystem.Connector
}

// New 创建工具调用处理，scopes 提供运行所属会话的锁定、事件唤醒与挂起运行的恢复，业务系统调用使用默认连接器。
func New(db *bun.DB, enqueuer servertask.TxEnqueuer, scopes RunScopes) *Action {
	return &Action{db: db, enqueuer: enqueuer, scopes: scopes, connector: businesssystem.NewDefaultConnector()}
}

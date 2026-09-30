//go:build server

package integrationtest

import (
	"context"

	agentrunaction "github.com/runforyou-ai/cervi/internal/actions/agentrun"
	groupchataction "github.com/runforyou-ai/cervi/internal/actions/groupchat"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
	"github.com/uptrace/bun"
)

// newGroupAgentTasks 创建已注册 Agent 运行 Action 的任务运行时。
func newGroupAgentTasks(db *bun.DB) *servertask.Runtime {
	tasks := newTestTasks(db)
	_ = tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil })
	return tasks
}

// newGroupSendAction 创建接入真实输入调度的群聊发送操作。
func newGroupSendAction(db *bun.DB) *groupchataction.SendGroupTextMessageAction {
	return groupchataction.NewSendGroupTextMessageAction(db, agentrunaction.NewScheduler(newGroupAgentTasks(db)))
}

// newGroupAgentCoordinator 创建群成员变化事务使用的 Agent 执行收敛器。
func newGroupAgentCoordinator(db *bun.DB) *agentrunaction.ExecuteAction {
	return agentrunaction.NewExecuteAction(db, newGroupAgentTasks(db), nil, testAttachmentReader(db), nil, nil)
}

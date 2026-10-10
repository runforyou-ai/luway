//go:build server

package integrationtest

import (
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	"github.com/runforyou-ai/luway/internal/servertest"
	"github.com/uptrace/bun"
)

// newGroupAgentTasks 返回群聊 Agent 运行投递使用的共用任务登记器。
func newGroupAgentTasks(db *bun.DB) *servertest.Tasks {
	return testEnqueuer
}

// newGroupSendAction 创建接入真实输入调度的群聊发送操作。
func newGroupSendAction(db *bun.DB) *groupchataction.SendGroupTextMessageAction {
	return groupchataction.NewSendGroupTextMessageAction(db, testEnqueuer, agentrunaction.NewScheduler(newGroupAgentTasks(db)))
}

// newGroupAgentCoordinator 创建群成员变化事务使用的 Agent 执行收敛器。
func newGroupAgentCoordinator(db *bun.DB) *testAgentRun {
	return newTestAgentRun(db, newGroupAgentTasks(db), nil, testModelInvoker(db), testAttachmentReader(db), nil, servertest.DisabledMail{}, nil, nil)
}

//go:build server

package integrationtest

import (
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	"github.com/runforyou-ai/luway/internal/actions/conversationfile"
	"github.com/runforyou-ai/luway/internal/actions/customernotify"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	servicehandoffaction "github.com/runforyou-ai/luway/internal/actions/servicehandoff"
	tooldecisionaction "github.com/runforyou-ai/luway/internal/actions/tooldecision"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// testAgentRun 组合测试使用的 Agent 运行执行、运行停止与取消、服务周期退回与转人工承接处理，它们共用同一数据库与任务投递。
type testAgentRun struct {
	*agentrunaction.ExecuteAction
	*agentrunaction.RunCancellation
	*servicehandoffaction.Returner
	*servicehandoffaction.ReturnedHandoffAction
}

// newTestAgentRun 按 Agent 运行执行的构造参数创建测试用的运行执行、停止与取消、服务周期退回与转人工承接处理。
func newTestAgentRun(db *bun.DB, enqueuer servertask.TxEnqueuer, runtime agentruntime.Runtime, invoker *modelcall.Invoker, attachments *agentrunaction.AttachmentReader,
	knowledge agentrunaction.KnowledgeRetrieval, emailSender customernotify.Sender, sharedFiles *conversationfile.Store, documents agentrunaction.DocumentConverter) *testAgentRun {
	return &testAgentRun{
		ExecuteAction:         agentrunaction.NewExecuteAction(db, enqueuer, runtime, invoker, attachments, knowledge, emailSender, sharedFiles, documents),
		RunCancellation:       agentrunaction.NewRunCancellation(db, enqueuer),
		Returner:              servicehandoffaction.NewReturner(enqueuer),
		ReturnedHandoffAction: servicehandoffaction.NewReturnedHandoffAction(db, enqueuer, emailSender),
	}
}

// newTestToolDecisions 创建测试用的工具决定处理，运行所属会话的锁定与事件唤醒由 Agent 运行提供。
func newTestToolDecisions(db *bun.DB, enqueuer servertask.TxEnqueuer) *tooldecisionaction.Action {
	return tooldecisionaction.New(db, enqueuer, agentrunaction.NewRunScopes(enqueuer))
}

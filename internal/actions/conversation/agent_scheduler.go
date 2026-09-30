//go:build server

package conversation

import (
	"context"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// GroupAgentMessageScheduler 把群内点名的 AI 员工消息加入持久化输入流。
type GroupAgentMessageScheduler interface {
	ScheduleGroupMentions(ctx context.Context, db bun.IDB, organizationID, conversationID, messageID, senderSubjectID string, agentIdentityIDs []string) error
}

// AgentChatMessageScheduler 把 AI 聊天与 Copilot 线程的成员消息按输入入口加入持久化输入流，服务周期内的发起人消息加入负责 AI 员工的服务输入流。
type AgentChatMessageScheduler interface {
	Schedule(ctx context.Context, db bun.IDB, organizationID, conversationID, agentIdentityID, revisionID, messageID, senderSubjectID string, kind domain.AgentInputKind) error
	CustomerAgentMessageScheduler
}

// CustomerAgentMessageScheduler 把渠道客户消息加入当前 AI 客服的持久输入流。
type CustomerAgentMessageScheduler interface {
	ScheduleCustomerAuto(ctx context.Context, db bun.IDB, organizationID, conversationID, serviceSessionID, messageID string) (bool, error)
}

// AgentMessageScheduler 调度 AI 聊天与 Copilot 线程的成员消息、渠道客户消息和群内点名的 Agent 输入。
type AgentMessageScheduler interface {
	AgentChatMessageScheduler
	GroupAgentMessageScheduler
}

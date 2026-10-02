//go:build server

package agentrun

import (
	"context"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// agentChatRunPolicy 定义独立 AI 聊天的运行策略，首条文本回复后投递标题任务。
type agentChatRunPolicy struct {
	enqueuer servertask.TxEnqueuer
}

// lockContext 锁定 AI 会话及其固定 Agent 的有效参与关系。
func (p agentChatRunPolicy) lockContext(ctx context.Context, db bun.IDB, run *servermodels.AgentRun) (agentRunPolicyContext, error) {
	cv, err := chatstate.LockConversation(ctx, db, run.OrganizationID, run.ConversationID)
	if err != nil {
		return agentRunPolicyContext{}, err
	}
	if cv.Type != string(domain.ConversationTypeAgent) {
		return agentRunPolicyContext{}, errors.New("agent run does not belong to an AI conversation")
	}
	// 校验已提交输入的固定归属和发送关系。
	var participantID string
	if err := db.NewSelect().TableExpr("conversation_participants AS cp").
		ColumnExpr("cp.id").
		Join("JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.organization_id = cp.organization_id").
		Join("JOIN agent_conversations AS ac ON ac.conversation_id = cp.conversation_id AND ac.organization_id = cp.organization_id AND ac.agent_identity_id = cs.source_id").
		Where("cp.organization_id = ? AND cp.conversation_id = ?", run.OrganizationID, run.ConversationID).
		Where("cp.left_at IS NULL AND cs.kind = ? AND ac.agent_identity_id = ?", domain.ChatSubjectKindOrganizationIdentity, run.AgentIdentityID).
		For("UPDATE OF cp").Scan(ctx, &participantID); err != nil {
		return agentRunPolicyContext{}, fmt.Errorf("lock AI conversation agent participant: %w", err)
	}
	return agentRunPolicyContext{Conversation: cv, AgentParticipantID: participantID}, nil
}

// prepareLocked 确认 AI 聊天 Agent 可以继续执行。
func (p agentChatRunPolicy) prepareLocked(context.Context, bun.IDB, agentRunPolicyContext, *servermodels.AgentRun) (bool, error) {
	return true, nil
}

// loadMessages 按会话稳定顺序读取 AI 聊天 Agent 上下文。
func (p agentChatRunPolicy) loadMessages(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, endSeq int64, links serverfilecontent.Links) ([]agentruntime.Message, error) {
	return loadClaimedConversationMessages(ctx, db, run, endSeq, links, false)
}

// persistMessage 追加独立 AI 会话的结果消息，新写入的文本回复是 AI 员工首条文本回复时投递标题任务，运行的 Agent 是个人 AI 员工时投递记忆提取任务。
func (p agentChatRunPolicy) persistMessage(ctx context.Context, db bun.IDB, policyContext agentRunPolicyContext, run *servermodels.AgentRun, messageID string, messageType domain.MessageType, content string) error {
	_, inserted, err := appendAgentMessage(ctx, db, policyContext.Conversation, agentResultMessage(run, messageID, policyContext.AgentParticipantID, messageType, content, nil))
	if err != nil || !inserted || messageType != domain.MessageTypeText {
		return err
	}
	if err := enqueueAgentChatTitle(ctx, db, p.enqueuer, policyContext.Conversation, policyContext.AgentParticipantID, messageID); err != nil {
		return err
	}
	return enqueueAgentMemory(ctx, db, p.enqueuer, run, messageID)
}

// sceneContext 给出企业内部对话场景。
func (p agentChatRunPolicy) sceneContext(context.Context, bun.IDB, executionContext) (agentruntime.SceneContext, error) {
	return agentruntime.SceneContext{Scene: agentruntime.SceneAgentChat}, nil
}

// laneRevision 读取 AI 聊天 Agent 当前生效的配置版本。
func (p agentChatRunPolicy) laneRevision(ctx context.Context, db bun.IDB, _ agentRunPolicyContext, lane *servermodels.AgentLane) (string, bool, error) {
	var revisionID string
	if err := db.NewSelect().Model((*servermodels.Agent)(nil)).
		Column("active_revision_id").
		Where("a.identity_id = ?", lane.AgentIdentityID).
		Where("a.organization_id = ?", lane.OrganizationID).
		Scan(ctx, &revisionID); err != nil {
		return "", false, fmt.Errorf("load next agent run revision: %w", err)
	}
	return revisionID, true, nil
}

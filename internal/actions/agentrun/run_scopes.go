//go:build server

package agentrun

import (
	"context"
	"fmt"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/businesssystem"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/tooldecision"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// 编译期确认 RunScopes 满足工具决定所需的运行能力。
var _ tooldecision.RunScopes = (*RunScopes)(nil)

// RunScopes 为工具决定按运行策略锁定运行所属会话、以结果事件唤醒执行范围、恢复挂起的运行并读取运行的可信上下文值。
type RunScopes struct {
	enqueuer servertask.TxEnqueuer
}

// NewRunScopes 创建工具决定使用的运行能力。
func NewRunScopes(enqueuer servertask.TxEnqueuer) *RunScopes {
	return &RunScopes{enqueuer: enqueuer}
}

// Lock 按运行的执行范围选择运行策略并锁定运行所属会话上下文。
func (s *RunScopes) Lock(ctx context.Context, tx bun.Tx, run *servermodels.AgentRun) (tooldecision.RunScope, error) {
	policy, err := runPolicy(ctx, tx, s.enqueuer, run)
	if err != nil {
		return nil, err
	}
	policyContext, err := policy.lockContext(ctx, tx, run)
	if err != nil {
		return nil, err
	}
	return lockedRunScope{enqueuer: s.enqueuer, policy: policy, policyContext: policyContext}, nil
}

// ResumeWaitingRun 在挂起运行的工具调用全部有结果时把运行改回排队并投递服务端任务。
func (s *RunScopes) ResumeWaitingRun(ctx context.Context, db bun.IDB, workspaceID, runID string) error {
	return ResumeWaitingRun(ctx, db, s.enqueuer, workspaceID, runID)
}

// RunValues 读取运行能提供的可信上下文值。
func (s *RunScopes) RunValues(ctx context.Context, db bun.IDB, run *servermodels.AgentRun) (businesssystem.RunValues, error) {
	return loadRunValues(ctx, db, run)
}

// lockedRunScope 是按运行策略锁定的会话上下文及其运行策略。
type lockedRunScope struct {
	enqueuer      servertask.TxEnqueuer
	policy        agentRunPolicy
	policyContext agentRunPolicyContext
}

// Conversation 返回锁定的会话。
func (s lockedRunScope) Conversation() *servermodels.Conversation {
	return s.policyContext.Conversation
}

// ServiceSession 返回锁定的服务周期，非服务周期执行范围为空。
func (s lockedRunScope) ServiceSession() *servermodels.ServiceSession {
	return s.policyContext.ServiceSession
}

// AgentParticipantID 返回锁定时取得的 AI 员工参与者，未取得时为空。
func (s lockedRunScope) AgentParticipantID() string {
	return s.policyContext.AgentParticipantID
}

// Wake 以 AI 员工的聊天主体为来源把结果事件消息作为事件输入追加到运行所属执行范围，并安排执行范围的下一次运行；执行范围已失去执行资格时只保留事件。
func (s lockedRunScope) Wake(ctx context.Context, tx bun.Tx, run *servermodels.AgentRun, messageID string) error {
	subject, err := chatstate.EnsureSubject(ctx, tx, run.WorkspaceID, domain.ChatSubjectKindWorkspaceIdentity, run.AgentIdentityID, uuid.NewV7().String())
	if err != nil {
		return err
	}
	spec := agentRunSpec{
		WorkspaceID: run.WorkspaceID, ConversationID: run.ConversationID, AgentIdentityID: run.AgentIdentityID,
		ScopeKind: domain.AgentExecutionScopeKind(run.ScopeKind), ScopeID: run.ScopeID,
		Kind: domain.AgentInputKindEvent, SourceSubjectID: subject.ID,
	}
	sequence, err := advanceLaneSequence(ctx, tx, spec)
	if err != nil {
		return err
	}
	if _, err := tx.NewInsert().Model(&servermodels.AgentInput{
		ID: uuid.NewV7().String(), WorkspaceID: run.WorkspaceID, LaneID: sequence.LaneID, InputSeq: sequence.DesiredSeq,
		Kind: string(spec.Kind), SourceMessageID: messageID, SourceSubjectID: subject.ID,
	}).Column("id", "workspace_id", "lane_id", "input_seq", "kind", "source_message_id", "source_subject_id", "source_ordinal").Exec(ctx); err != nil {
		return fmt.Errorf("create agent event input: %w", err)
	}
	return scheduleNextRun(ctx, tx, s.enqueuer, s.policy, s.policyContext, run.WorkspaceID, spec.ScopeKind, spec.ScopeID)
}

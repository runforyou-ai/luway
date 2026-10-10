//go:build server

package agentrun

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// Scheduler 在消息事务内追加 Agent 输入并派发运行。
type Scheduler struct {
	enqueuer servertask.TxEnqueuer
}

// laneSequence 是输入队列分配序号后的队列编号、期望序号与已处理序号。
type laneSequence struct {
	LaneID       string `bun:"lane_id"`
	DesiredSeq   int64  `bun:"desired_seq"`
	ProcessedSeq int64  `bun:"processed_seq"`
}

// agentRunSpec 定义一次追加输入或创建运行所需的会话、AI 员工、配置版本、执行范围与输入来源。
type agentRunSpec struct {
	WorkspaceID     string
	ConversationID  string
	AgentIdentityID string
	RevisionID      string
	ScopeKind       domain.AgentExecutionScopeKind
	ScopeID         string
	Kind            domain.AgentInputKind
	SourceSubjectID string
	SourceOrdinal   int
}

// NewScheduler 创建 Agent 运行调度器。
func NewScheduler(enqueuer servertask.TxEnqueuer) *Scheduler {
	return &Scheduler{enqueuer: enqueuer}
}

// Schedule 在调用方已锁定会话的事务内追加 AI 聊天或 Copilot 线程的成员输入，执行范围为该会话。
func (s *Scheduler) Schedule(ctx context.Context, db bun.IDB, workspaceID, conversationID, agentIdentityID, revisionID, messageID, senderSubjectID string, kind domain.AgentInputKind) error {
	return s.appendInput(ctx, db, agentRunSpec{
		WorkspaceID: workspaceID, ConversationID: conversationID,
		AgentIdentityID: agentIdentityID, RevisionID: revisionID,
		ScopeKind: domain.AgentExecutionScopeConversation, ScopeID: conversationID,
		Kind: kind, SourceSubjectID: senderSubjectID,
	}, messageID)
}

// appendInput 追加一条持久输入并确保对应执行范围已有在途运行。
func (s *Scheduler) appendInput(ctx context.Context, db bun.IDB, spec agentRunSpec, messageID string) error {
	if s.enqueuer == nil {
		return errors.New("agent run scheduler is unavailable")
	}
	sequence, err := advanceLaneSequence(ctx, db, spec)
	if err != nil {
		return err
	}
	input := &servermodels.AgentInput{
		ID: uuid.NewV7().String(), WorkspaceID: spec.WorkspaceID, LaneID: sequence.LaneID,
		InputSeq: sequence.DesiredSeq, Kind: string(spec.Kind),
		SourceMessageID: messageID, SourceSubjectID: spec.SourceSubjectID, SourceOrdinal: spec.SourceOrdinal,
	}
	if _, err := db.NewInsert().Model(input).
		Column("id", "workspace_id", "lane_id", "input_seq", "kind", "source_message_id", "source_subject_id", "source_ordinal").
		Exec(ctx); err != nil {
		return fmt.Errorf("create agent input: %w", err)
	}
	active, err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).
		Where("agr.workspace_id = ?", spec.WorkspaceID).
		Where("agr.scope_kind = ? AND agr.scope_id = ?", spec.ScopeKind, spec.ScopeID).
		Where("agr.status IN (?)", bun.List(domain.AgentRunActiveStatuses)).
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("check active agent run: %w", err)
	}
	if active {
		return nil
	}
	_, err = insertAndDispatchRun(ctx, db, s.enqueuer, spec, sequence.LaneID, sequence.ProcessedSeq+1)
	return err
}

// advanceLaneSequence 建立或锁定执行范围的输入队列并分配下一条输入序号，提交后向正在执行该队列运行的实例发出新增输入信号；调用方必须处于 realtime.RunInTx 内。
func advanceLaneSequence(ctx context.Context, db bun.IDB, spec agentRunSpec) (laneSequence, error) {
	sequence := laneSequence{}
	if err := db.NewRaw(`
		INSERT INTO agent_lanes (
			workspace_id, conversation_id, agent_identity_id, scope_kind, scope_id, desired_seq, processed_seq
		)
		VALUES (?, ?, ?, ?, ?, 1, 0)
		ON CONFLICT (workspace_id, scope_kind, scope_id, agent_identity_id) DO UPDATE
		SET desired_seq = agent_lanes.desired_seq + 1
		RETURNING id AS lane_id, desired_seq, processed_seq
	`, spec.WorkspaceID, spec.ConversationID, spec.AgentIdentityID, spec.ScopeKind, spec.ScopeID).
		Scan(ctx, &sequence); err != nil {
		return laneSequence{}, fmt.Errorf("advance agent lane input sequence: %w", err)
	}
	realtime.Notify(ctx, realtime.AgentLaneInputAdded(spec.WorkspaceID, sequence.LaneID))
	return sequence, nil
}

// insertAndDispatchRun 创建 Agent 业务运行并投递隔离 Worker 执行。
func insertAndDispatchRun(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, spec agentRunSpec, laneID string, startSeq int64) (string, error) {
	run := &servermodels.AgentRun{
		ID: uuid.NewV7().String(), WorkspaceID: spec.WorkspaceID, ConversationID: spec.ConversationID,
		AgentIdentityID: spec.AgentIdentityID, AgentRevisionID: spec.RevisionID, LaneID: laneID,
		ScopeKind: string(spec.ScopeKind), ScopeID: spec.ScopeID,
		Status: string(domain.AgentRunStatusQueued), InputStartSeq: startSeq,
	}
	if _, err := db.NewInsert().Model(run).
		Column("id", "workspace_id", "conversation_id", "agent_identity_id", "agent_revision_id", "lane_id", "scope_kind", "scope_id", "status", "input_start_seq").
		Exec(ctx); err != nil {
		return "", fmt.Errorf("create agent run: %w", err)
	}
	if _, err := enqueuer.EnqueueIn(ctx, RunActionName, RunInput{RunID: run.ID}, servertask.EnqueueOptions{
		WorkspaceID: spec.WorkspaceID, Queue: servertask.QueueAgent, MaxAttempts: 3,
		IdempotencyKey: "agent:" + run.ID,
	}); err != nil {
		return "", fmt.Errorf("enqueue agent run: %w", err)
	}
	return run.ID, nil
}

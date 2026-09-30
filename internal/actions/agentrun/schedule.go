//go:build server

package agentrun

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
	"github.com/uptrace/bun"
)

// Scheduler 在消息事务内追加 Agent 输入并派发运行。
type Scheduler struct {
	enqueuer servertask.TxEnqueuer
}

type laneSequence struct {
	LaneID       string `bun:"lane_id"`
	DesiredSeq   int64  `bun:"desired_seq"`
	ProcessedSeq int64  `bun:"processed_seq"`
}

type agentRunSpec struct {
	OrganizationID  string
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
func (s *Scheduler) Schedule(ctx context.Context, db bun.IDB, organizationID, conversationID, agentIdentityID, revisionID, messageID, senderSubjectID string, kind domain.AgentInputKind) error {
	return s.appendInput(ctx, db, agentRunSpec{
		OrganizationID: organizationID, ConversationID: conversationID,
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
		ID: uuid.NewV7().String(), OrganizationID: spec.OrganizationID, LaneID: sequence.LaneID,
		InputSeq: sequence.DesiredSeq, Kind: string(spec.Kind),
		SourceMessageID: messageID, SourceSubjectID: spec.SourceSubjectID, SourceOrdinal: spec.SourceOrdinal,
	}
	if _, err := db.NewInsert().Model(input).
		Column("id", "organization_id", "lane_id", "input_seq", "kind", "source_message_id", "source_subject_id", "source_ordinal").
		Exec(ctx); err != nil {
		return fmt.Errorf("create agent input: %w", err)
	}
	active, err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).
		Where("agr.organization_id = ?", spec.OrganizationID).
		Where("agr.scope_kind = ? AND agr.scope_id = ?", spec.ScopeKind, spec.ScopeID).
		Where("agr.status IN (?, ?)", domain.AgentRunStatusQueued, domain.AgentRunStatusRunning).
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

// advanceLaneSequence 建立或锁定执行范围的输入队列并分配下一条输入序号。
func advanceLaneSequence(ctx context.Context, db bun.IDB, spec agentRunSpec) (laneSequence, error) {
	sequence := laneSequence{}
	if err := db.NewRaw(`
		INSERT INTO agent_lanes (
			organization_id, conversation_id, agent_identity_id, scope_kind, scope_id, desired_seq, processed_seq
		)
		VALUES (?, ?, ?, ?, ?, 1, 0)
		ON CONFLICT (organization_id, scope_kind, scope_id, agent_identity_id) DO UPDATE
		SET desired_seq = agent_lanes.desired_seq + 1,
			updated_at = now()
		RETURNING id AS lane_id, desired_seq, processed_seq
	`, spec.OrganizationID, spec.ConversationID, spec.AgentIdentityID, spec.ScopeKind, spec.ScopeID).
		Scan(ctx, &sequence); err != nil {
		return laneSequence{}, fmt.Errorf("advance agent lane input sequence: %w", err)
	}
	return sequence, nil
}

// insertAndDispatchRun 创建 Agent 业务运行并派发执行：助理的运行交给其绑定电脑，AI 员工的运行投递隔离 Worker。
func insertAndDispatchRun(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, spec agentRunSpec, laneID string, startSeq int64) (string, error) {
	run := &servermodels.AgentRun{
		ID: uuid.NewV7().String(), OrganizationID: spec.OrganizationID, ConversationID: spec.ConversationID,
		AgentIdentityID: spec.AgentIdentityID, AgentRevisionID: spec.RevisionID, LaneID: laneID,
		ScopeKind: string(spec.ScopeKind), ScopeID: spec.ScopeID,
		Status: string(domain.AgentRunStatusQueued), InputStartSeq: startSeq,
	}
	if err := db.NewSelect().Model((*servermodels.Agent)(nil)).
		Column("a.device_id").
		Where("a.organization_id = ? AND a.identity_id = ?", spec.OrganizationID, spec.AgentIdentityID).
		Scan(ctx, &run.ExecutionDeviceID); err != nil {
		return "", fmt.Errorf("load agent execution device: %w", err)
	}
	onDevice := run.ExecutionDeviceID != nil
	if _, err := db.NewInsert().Model(run).
		Column("id", "organization_id", "conversation_id", "agent_identity_id", "agent_revision_id", "lane_id", "scope_kind", "scope_id", "status", "input_start_seq",
			"execution_device_id").
		Exec(ctx); err != nil {
		return "", fmt.Errorf("create agent run: %w", err)
	}
	if onDevice {
		return run.ID, advanceDeviceWork(ctx, db, spec.OrganizationID, *run.ExecutionDeviceID)
	}
	if _, err := enqueuer.EnqueueIn(ctx, db, RunActionName, RunInput{RunID: run.ID}, servertask.EnqueueOptions{
		Queue: servertask.QueueAgent, MaxAttempts: 3,
		IdempotencyKey: "agent:" + run.ID,
		TriggerType:    servertask.TriggerBusiness,
	}); err != nil {
		return "", fmt.Errorf("enqueue agent run: %w", err)
	}
	return run.ID, nil
}

// advanceDeviceWork 推进设备工作水位，并在事务提交后通知设备主人的该设备事件流；调用方必须处于 realtime.RunInTx 内。
func advanceDeviceWork(ctx context.Context, db bun.IDB, organizationID, deviceID string) error {
	var advanced struct {
		UserID  string `bun:"user_id"`
		WorkSeq int64  `bun:"work_seq"`
	}
	if err := db.NewRaw(`
		UPDATE devices SET work_seq = work_seq + 1, updated_at = now()
		WHERE organization_id = ? AND id = ?
		RETURNING user_id, work_seq
	`, organizationID, deviceID).Scan(ctx, &advanced); err != nil {
		return fmt.Errorf("advance device work sequence: %w", err)
	}
	realtime.Notify(ctx, realtime.UserDeviceWorkAdvanced(organizationID, advanced.UserID, deviceID, advanced.WorkSeq))
	return nil
}

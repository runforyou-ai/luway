//go:build server

package server_test

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/modelcall"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// TestAgentCallbacksFenceTaskAttempts 验证开始执行和最终失败均拒绝旧任务尝试，并保持消息与消费状态原子提交。
func TestAgentCallbacksFenceTaskAttempts(t *testing.T) {
	ctx, db, tasks := servertask.NewExecutionRuntimeForTest(t)
	run := seedAgentExecution(t, ctx, db)
	if err := tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	taskID, err := tasks.Enqueue(ctx, agentrunaction.RunActionName, agentrunaction.RunInput{RunID: run.ID}, servertask.EnqueueOptions{MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.NewDelete().Model((*servermodels.TaskOutbox)(nil)).Where("task_run_id = ?", taskID).Exec(context.Background())
		_, _ = db.NewDelete().Model((*servermodels.TaskRun)(nil)).Where("id = ?", taskID).Exec(context.Background())
	})
	var current servermodels.TaskRun
	if err := db.NewUpdate().Model(&current).
		Set("status = 'running'").Set("attempt = 2").Set("worker_id = 'current-worker'").
		Set("lease_expires_at = now() + interval '1 hour'").Where("id = ?", taskID).Returning("*").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	executor := agentrunaction.NewExecuteAction(db, tasks, nil, modelcall.New(db, modelcall.DefaultUpstreams()), agentrunaction.NewAttachmentReader(db, nil, serverfilecontent.NewLinks("http", "")), nil, nil)
	old := current
	old.Attempt = 1
	oldWorker := "old-worker"
	old.WorkerID = &oldWorker
	for _, start := range []bool{true, false} {
		oldCtx := servertask.WithExecutionForTest(ctx, &old, !start, false)
		if start {
			err = executor.Execute(oldCtx, agentrunaction.RunInput{RunID: run.ID})
		} else {
			err = executor.FinalizeFailure(oldCtx, agentrunaction.RunInput{RunID: run.ID}, errors.New("old attempt failed"))
		}
		if !errors.Is(err, servertask.ErrExecutionLost) {
			t.Fatalf("old callback start=%v err=%v", start, err)
		}
		assertAgentExecutionUnchanged(t, ctx, db, run)
	}
	// 核验 Worker 和尝试匹配但租约过期时的执行限制。
	if _, err := db.NewUpdate().Model((*servermodels.TaskRun)(nil)).Set("lease_expires_at = now() - interval '1 second'").Where("id = ?", taskID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	expiredCtx := servertask.WithExecutionForTest(ctx, &current, false, false)
	for _, callback := range []func() error{
		func() error { return executor.Execute(expiredCtx, agentrunaction.RunInput{RunID: run.ID}) },
		func() error {
			return executor.FinalizeFailure(expiredCtx, agentrunaction.RunInput{RunID: run.ID}, errors.New("expired result"))
		},
	} {
		if err := callback(); !errors.Is(err, servertask.ErrExecutionLost) {
			t.Fatalf("expired callback=%v", err)
		}
		assertAgentExecutionUnchanged(t, ctx, db, run)
	}
	// 当前最终失败回调保留任务系统的收尾语义，允许收敛过期租约。
	currentCtx := servertask.WithExecutionForTest(ctx, &current, true, false)
	if err := executor.FinalizeFailure(currentCtx, agentrunaction.RunInput{RunID: run.ID}, errors.New("current attempt failed")); err != nil {
		t.Fatal(err)
	}
	if err := executor.FinalizeFailure(servertask.WithExecutionForTest(ctx, &old, true, false), agentrunaction.RunInput{RunID: run.ID}, errors.New("late old callback")); err != nil {
		t.Fatal(err)
	}
	if err := db.NewSelect().Model(&run).WherePK().Scan(ctx); err != nil || run.Status != string(domain.AgentRunStatusFailed) || run.ResponseMessageID == nil {
		t.Fatalf("terminal run=%+v err=%v", run, err)
	}
	var message servermodels.Message
	if err := db.NewSelect().Model(&message).Where("msg.id = ?", *run.ResponseMessageID).Scan(ctx); err != nil || message.Type != string(domain.MessageTypeAgentError) || message.ConversationID != run.ConversationID {
		t.Fatalf("result message=%+v err=%v", message, err)
	}
	var state servermodels.AgentLane
	if err := db.NewSelect().Model(&state).Where("al.conversation_id = ?", run.ConversationID).Scan(ctx); err != nil || state.ProcessedSeq != 1 {
		t.Fatalf("consumed state=%+v err=%v", state, err)
	}
	count, err := db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.idempotency_key = ?", "agent:"+run.ID).Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("result messages=%d err=%v", count, err)
	}
}

// TestAgentCallbacksRespectRunOwner 验证运行已登记由其他任务执行时，当前任务的开始执行与最终失败都不改变运行。
func TestAgentCallbacksRespectRunOwner(t *testing.T) {
	ctx, db, tasks := servertask.NewExecutionRuntimeForTest(t)
	run := seedAgentExecution(t, ctx, db)
	if err := tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	taskID, err := tasks.Enqueue(ctx, agentrunaction.RunActionName, agentrunaction.RunInput{RunID: run.ID}, servertask.EnqueueOptions{MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.NewDelete().Model((*servermodels.TaskOutbox)(nil)).Where("task_run_id = ?", taskID).Exec(context.Background())
		_, _ = db.NewDelete().Model((*servermodels.TaskRun)(nil)).Where("id = ?", taskID).Exec(context.Background())
	})
	var current servermodels.TaskRun
	if err := db.NewUpdate().Model(&current).
		Set("status = 'running'").Set("attempt = 1").Set("worker_id = 'current-worker'").
		Set("lease_expires_at = now() + interval '1 hour'").Where("id = ?", taskID).Returning("*").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	executor := agentrunaction.NewExecuteAction(db, tasks, nil, modelcall.New(db, modelcall.DefaultUpstreams()), agentrunaction.NewAttachmentReader(db, nil, serverfilecontent.NewLinks("http", "")), nil, nil)
	// 恢复投递后排队等待的运行与执行中的运行，都只由登记的任务处理。
	for _, status := range []domain.AgentRunStatus{domain.AgentRunStatusQueued, domain.AgentRunStatusRunning} {
		owner := uuid.NewV7().String()
		if _, err := db.NewUpdate().Model((*servermodels.AgentRun)(nil)).
			Set("status = ?", status).Set("task_run_id = ?", owner).Set("started_at = now()").
			Where("id = ?", run.ID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if err := executor.Execute(servertask.WithExecutionForTest(ctx, &current, false, false), agentrunaction.RunInput{RunID: run.ID}); err != nil {
			t.Fatal(err)
		}
		if err := executor.FinalizeFailure(servertask.WithExecutionForTest(ctx, &current, true, false), agentrunaction.RunInput{RunID: run.ID}, errors.New("stale task failed")); err != nil {
			t.Fatal(err)
		}
		if err := db.NewSelect().Model(&run).WherePK().Scan(ctx); err != nil || run.Status != string(status) || run.TaskRunID == nil || *run.TaskRunID != owner {
			t.Fatalf("%s run owned by another task changed: %+v err=%v", status, run, err)
		}
	}
}

// seedAgentExecution 构造 AI 会话、参与关系和单条待消费输入。
func seedAgentExecution(t *testing.T, ctx context.Context, db *bun.DB) servermodels.AgentRun {
	t.Helper()
	organizationID, userID, agentID, conversationID := uuid.NewV7().String(), uuid.NewV7().String(), uuid.NewV7().String(), uuid.NewV7().String()
	laneID := uuid.NewV7().String()
	run := servermodels.AgentRun{ID: uuid.NewV7().String(), OrganizationID: organizationID, ConversationID: conversationID, AgentIdentityID: agentID, AgentRevisionID: uuid.NewV7().String(), LaneID: laneID, ScopeKind: string(domain.AgentExecutionScopeConversation), ScopeID: conversationID, Status: string(domain.AgentRunStatusQueued), InputStartSeq: 1}
	t.Cleanup(func() {
		for _, table := range []string{"messages", "agent_inputs", "agent_runs", "agent_lanes", "conversation_participants", "agent_conversations", "conversations", "chat_subjects"} {
			if _, err := db.NewDelete().TableExpr(table).Where("organization_id = ?", organizationID).Exec(context.Background()); err != nil {
				t.Error(err)
			}
		}
	})
	err := realtime.RunInTx(ctx, db, func(ctx context.Context, tx bun.Tx) error {
		cv := &servermodels.Conversation{ID: conversationID, OrganizationID: organizationID, Type: string(domain.ConversationTypeAgent), Status: string(domain.ConversationStatusActive)}
		if _, err := tx.NewInsert().Model(cv).Column("id", "organization_id", "type", "status").Exec(ctx); err != nil {
			return err
		}
		relation := &servermodels.AgentConversation{ConversationID: conversationID, OrganizationID: organizationID, UserIdentityID: userID, AgentIdentityID: agentID}
		if _, err := tx.NewInsert().Model(relation).Column("conversation_id", "organization_id", "user_identity_id", "agent_identity_id").Exec(ctx); err != nil {
			return err
		}
		var userParticipantID string
		for _, identityID := range []string{userID, agentID} {
			subject := &servermodels.ChatSubject{ID: uuid.NewV7().String(), OrganizationID: organizationID, Kind: string(domain.ChatSubjectKindOrganizationIdentity), SourceID: identityID}
			if _, err := tx.NewInsert().Model(subject).Column("id", "organization_id", "kind", "source_id").Exec(ctx); err != nil {
				return err
			}
			participant := &servermodels.ConversationParticipant{ID: uuid.NewV7().String(), OrganizationID: organizationID, ConversationID: conversationID, SubjectID: subject.ID, Role: string(domain.ConversationParticipantRoleMember)}
			if _, err := tx.NewInsert().Model(participant).Column("id", "organization_id", "conversation_id", "subject_id", "role").Exec(ctx); err != nil {
				return err
			}
			if identityID == userID {
				userParticipantID = participant.ID
			}
		}
		message := &servermodels.Message{ID: uuid.NewV7().String(), OrganizationID: organizationID, ConversationID: conversationID, SenderParticipantID: &userParticipantID, Type: string(domain.MessageTypeText), Body: "待处理输入", OriginatedAt: time.Now().UTC()}
		if _, _, err := chatstate.AppendMessage(ctx, tx, cv, message); err != nil {
			return err
		}
		lane := &servermodels.AgentLane{ID: laneID, OrganizationID: organizationID, ConversationID: conversationID, AgentIdentityID: agentID, ScopeKind: string(domain.AgentExecutionScopeConversation), ScopeID: conversationID, DesiredSeq: 1}
		if _, err := tx.NewInsert().Model(lane).Column("id", "organization_id", "conversation_id", "agent_identity_id", "scope_kind", "scope_id", "desired_seq", "processed_seq").Exec(ctx); err != nil {
			return err
		}
		input := &servermodels.AgentInput{ID: uuid.NewV7().String(), OrganizationID: organizationID, LaneID: laneID, InputSeq: 1, Kind: string(domain.AgentInputKindAgentDirect), SourceMessageID: message.ID, SourceSubjectID: uuid.NewV7().String()}
		if _, err := tx.NewInsert().Model(input).Column("id", "organization_id", "lane_id", "input_seq", "kind", "source_message_id", "source_subject_id").Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewInsert().Model(&run).Column("id", "organization_id", "conversation_id", "agent_identity_id", "agent_revision_id", "lane_id", "scope_kind", "scope_id", "status", "input_start_seq").Exec(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

// assertAgentExecutionUnchanged 核对被租约拒绝的回调没有留下业务写入。
func assertAgentExecutionUnchanged(t *testing.T, ctx context.Context, db *bun.DB, run servermodels.AgentRun) {
	t.Helper()
	if err := db.NewSelect().Model(&run).WherePK().Scan(ctx); err != nil || run.Status != string(domain.AgentRunStatusQueued) || run.StartedAt != nil || run.ResponseMessageID != nil {
		t.Fatalf("run changed after rejected callback: %+v err=%v", run, err)
	}
	var state servermodels.AgentLane
	if err := db.NewSelect().Model(&state).Where("al.conversation_id = ?", run.ConversationID).Scan(ctx); err != nil || state.ProcessedSeq != 0 {
		t.Fatalf("state changed after rejected callback: %+v err=%v", state, err)
	}
	count, err := db.NewSelect().Model((*servermodels.Message)(nil)).Where("msg.idempotency_key = ?", "agent:"+run.ID).Count(ctx)
	if err != nil || count != 0 {
		t.Fatalf("rejected result messages=%d err=%v", count, err)
	}
}

// TestCustomerCallbacksFenceTaskAttempts 验证客服回调在当前周期锁后拒绝旧尝试、旧 Worker 和过期租约。
func TestCustomerCallbacksFenceTaskAttempts(t *testing.T) {
	ctx, db, tasks := servertask.NewExecutionRuntimeForTest(t)
	run := seedAgentExecution(t, ctx, db)
	sessionID, identityID, serviceConversationID := uuid.NewV7().String(), uuid.NewV7().String(), uuid.NewV7().String()
	t.Cleanup(func() {
		for _, table := range []string{"service_sessions", "service_conversations", "channel_conversations", "contact_channel_identities", "channels"} {
			if _, err := db.NewDelete().TableExpr(table).Where("organization_id = ?", run.OrganizationID).Exec(context.Background()); err != nil {
				t.Error(err)
			}
		}
	})
	channelID := uuid.NewV7().String()
	if _, err := db.ExecContext(ctx, "INSERT INTO channels (id, organization_id, created_by_user_id, type, name) VALUES (?, ?, ?, 'website', '客服任务围栏')", channelID, run.OrganizationID, uuid.NewV7().String()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO contact_channel_identities (id, organization_id, contact_id, channel_id, external_id) VALUES (?, ?, ?, ?, 'test-visitor')", identityID, run.OrganizationID, uuid.NewV7().String(), channelID); err != nil {
		t.Fatal(err)
	}
	// 已关闭周期使有效最终回调只收敛取消，不需要外部模型或执行配置。
	if _, err := db.ExecContext(ctx, "UPDATE conversations SET type = 'channel' WHERE id = ?", run.ConversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO channel_conversations (organization_id, conversation_id, contact_channel_identity_id) VALUES (?, ?, ?)", run.OrganizationID, run.ConversationID, identityID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO service_conversations (id, organization_id, conversation_id, source, requester_subject_id, audience, current_service_session_id) VALUES (?, ?, ?, 'channel', ?, 'customer', ?)", serviceConversationID, run.OrganizationID, run.ConversationID, uuid.NewV7().String(), sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO service_sessions (id, organization_id, conversation_id, service_conversation_id, sequence, status, opening_message_id, last_message_id, last_message_at, status_changed_at)
 SELECT ?, organization_id, conversation_id, ?, 1, 'closed', id, id, originated_at, now() FROM messages WHERE conversation_id = ?`, sessionID, serviceConversationID, run.ConversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE agent_runs SET scope_kind = ?, scope_id = ? WHERE id = ?", domain.AgentExecutionScopeServiceSession, sessionID, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := tasks.Registry().RegisterJSON(agentrunaction.RunActionName, func(context.Context, agentrunaction.RunInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	taskID, err := tasks.Enqueue(ctx, agentrunaction.RunActionName, agentrunaction.RunInput{RunID: run.ID}, servertask.EnqueueOptions{MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.NewDelete().Model((*servermodels.TaskOutbox)(nil)).Where("task_run_id = ?", taskID).Exec(context.Background())
		_, _ = db.NewDelete().Model((*servermodels.TaskRun)(nil)).Where("id = ?", taskID).Exec(context.Background())
	})
	var current servermodels.TaskRun
	if err := db.NewUpdate().Model(&current).Set("status = 'running'").Set("attempt = 2").Set("worker_id = 'customer-worker'").Set("lease_expires_at = now() + interval '1 hour'").Where("id = ?", taskID).Returning("*").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	executor := agentrunaction.NewExecuteAction(db, tasks, nil, modelcall.New(db, modelcall.DefaultUpstreams()), agentrunaction.NewAttachmentReader(db, nil, serverfilecontent.NewLinks("http", "")), nil, nil)
	for _, kind := range []string{"旧尝试", "旧Worker", "过期租约"} {
		stale := current
		switch kind {
		case "旧尝试":
			stale.Attempt = 1
		case "旧Worker":
			worker := "old-worker"
			stale.WorkerID = &worker
		case "过期租约":
			if _, err := db.NewUpdate().Model((*servermodels.TaskRun)(nil)).Set("lease_expires_at = now() - interval '1 second'").Where("id = ?", taskID).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		for _, start := range []bool{true, false} {
			staleCtx := servertask.WithExecutionForTest(ctx, &stale, false, false)
			if start {
				err = executor.Execute(staleCtx, agentrunaction.RunInput{RunID: run.ID})
			} else {
				err = executor.FinalizeFailure(staleCtx, agentrunaction.RunInput{RunID: run.ID}, errors.New("旧失败"))
			}
			if !errors.Is(err, servertask.ErrExecutionLost) {
				t.Fatalf("%s start=%v err=%v", kind, start, err)
			}
			assertAgentExecutionUnchanged(t, ctx, db, run)
		}
	}
	if err := executor.FinalizeFailure(servertask.WithExecutionForTest(ctx, &current, true, false), agentrunaction.RunInput{RunID: run.ID}, errors.New("最终失败")); err != nil {
		t.Fatal(err)
	}
	if err := db.NewSelect().Model(&run).WherePK().Scan(ctx); err != nil || run.Status != string(domain.AgentRunStatusCancelled) || run.ResponseMessageID != nil {
		t.Fatalf("run=%+v err=%v", run, err)
	}
}

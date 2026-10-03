//go:build server

package agentrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// runJournal 把运行在安全点的过程、用量、任务清单与恢复状态写入数据库。
type runJournal struct {
	db  *bun.DB
	run *servermodels.AgentRun
}

// SaveStep 在一个事务中写入全部过程内容并更新运行的用量、任务清单与恢复状态；运行已不处于执行中时只写入过程内容。
func (j *runJournal) SaveStep(ctx context.Context, step agentruntime.Step) error {
	usage, err := json.Marshal(step.Usage)
	if err != nil {
		return fmt.Errorf("encode agent run usage: %w", err)
	}
	plan, err := encodeRunPlan(step.Plan)
	if err != nil {
		return err
	}
	return j.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := agentprocess.Sync(ctx, tx, j.run, step.Blocks, step.Calls); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model((*servermodels.AgentRun)(nil)).
			Set("state = ?", step.State).
			Set("usage = ?::jsonb", string(usage)).
			Set("plan = ?::jsonb", plan).
			Set("updated_at = now()").
			Where("id = ? AND organization_id = ? AND status = ?", j.run.ID, j.run.OrganizationID, domain.AgentRunStatusRunning).
			Exec(ctx); err != nil {
			return fmt.Errorf("save agent run state: %w", err)
		}
		return nil
	})
}

// SaveToolCall 写入一次工具调用的当前状态。
func (j *runJournal) SaveToolCall(ctx context.Context, call agentruntime.ToolCall) error {
	return agentprocess.SaveToolCall(ctx, j.db, j.run, call)
}

// loadResume 读取运行已保存的恢复状态与过程内容，运行尚未保存恢复状态时返回空。
func loadResume(ctx context.Context, db bun.IDB, run *servermodels.AgentRun) (*agentruntime.Resume, error) {
	var state []byte
	if err := db.NewSelect().Model((*servermodels.AgentRun)(nil)).Column("state").
		Where("id = ? AND organization_id = ?", run.ID, run.OrganizationID).Scan(ctx, &state); err != nil {
		return nil, fmt.Errorf("load agent run state: %w", err)
	}
	if len(state) == 0 {
		return nil, nil
	}
	blocks, calls, err := agentprocess.Load(ctx, db, run.OrganizationID, run.ID)
	if err != nil {
		return nil, err
	}
	resume := &agentruntime.Resume{State: state, Blocks: blocks, Calls: calls}
	if len(run.Plan) > 0 {
		if err := json.Unmarshal(run.Plan, &resume.Plan); err != nil {
			return nil, fmt.Errorf("decode agent run plan: %w", err)
		}
	}
	return resume, nil
}

// suspend 把执行中的运行改为挂起并推进会话版本；等待的调用已全部有结果时立即投递恢复。
func (a *ExecuteAction) suspend(ctx context.Context, run *servermodels.AgentRun) error {
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		conversation, err := chatstate.LockConversation(ctx, tx, run.OrganizationID, run.ConversationID)
		if err != nil {
			return err
		}
		updated, err := tx.NewUpdate().Model((*servermodels.AgentRun)(nil)).
			Set("status = ?", domain.AgentRunStatusWaiting).
			Set("task_run_id = NULL").
			Set("updated_at = now()").
			Where("id = ? AND organization_id = ? AND status = ?", run.ID, run.OrganizationID, domain.AgentRunStatusRunning).
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("suspend agent run: %w", err)
		}
		if affected, err := updated.RowsAffected(); err != nil || affected == 0 {
			return err
		}
		if err := chatstate.TouchConversation(ctx, tx, conversation, domain.ConversationChangeTimeline); err != nil {
			return err
		}
		return ResumeWaitingRun(ctx, tx, a.enqueuer, run.OrganizationID, run.ID)
	})
}

// ResumeWaitingRun 在挂起运行等待的工具调用全部有结果时把运行改回排队、推进会话版本，并按原执行位置派发：设备执行的运行推进设备工作水位，其余投递服务端任务；
// 运行未挂起或仍有调用等待时不做任何事。调用方在写入调用结果的 realtime.RunInTx 事务中调用。
func ResumeWaitingRun(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, organizationID, runID string) error {
	initial := &servermodels.AgentRun{}
	err := db.NewSelect().Model(initial).Where("agr.id = ? AND agr.organization_id = ?", runID, organizationID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return fmt.Errorf("load waiting agent run: %w", err)
	}
	if initial.Status != string(domain.AgentRunStatusWaiting) {
		return nil
	}
	// 按会话、运行的顺序加锁。
	conversation, err := chatstate.LockConversation(ctx, db, organizationID, initial.ConversationID)
	if err != nil {
		return err
	}
	run := &servermodels.AgentRun{}
	if err := db.NewSelect().Model(run).Where("agr.id = ?", runID).For("UPDATE").Scan(ctx); err != nil {
		return fmt.Errorf("lock waiting agent run: %w", err)
	}
	if run.Status != string(domain.AgentRunStatusWaiting) {
		return nil
	}
	waiting, err := db.NewSelect().Model((*servermodels.AgentToolCall)(nil)).
		Where("organization_id = ? AND agent_run_id = ? AND status = ?", organizationID, runID, domain.AgentToolCallWaiting).
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("check waiting agent tool calls: %w", err)
	}
	if waiting {
		return nil
	}
	if _, err := db.NewUpdate().Model(run).
		Set("status = ?", domain.AgentRunStatusQueued).
		Set("updated_at = now()").
		WherePK().Exec(ctx); err != nil {
		return fmt.Errorf("requeue waiting agent run: %w", err)
	}
	if err := chatstate.TouchConversation(ctx, db, conversation, domain.ConversationChangeTimeline); err != nil {
		return err
	}
	if run.ExecutionDeviceID != nil {
		return advanceDeviceWork(ctx, db, organizationID, *run.ExecutionDeviceID)
	}
	// 每次挂起只在改回排队时投递一次，并把新任务登记为执行方，原任务之后的重试与失败收尾都不再处理该运行。
	taskRunID, err := enqueuer.EnqueueIn(ctx, db, RunActionName, RunInput{RunID: runID}, servertask.EnqueueOptions{
		OrganizationID: organizationID, Queue: servertask.QueueAgent, MaxAttempts: 3,
		TriggerType: servertask.TriggerBusiness,
	})
	if err != nil {
		return fmt.Errorf("enqueue agent run resume: %w", err)
	}
	if _, err := db.NewUpdate().Model(run).Set("task_run_id = ?", taskRunID).WherePK().Exec(ctx); err != nil {
		return fmt.Errorf("register agent run resume task: %w", err)
	}
	return nil
}

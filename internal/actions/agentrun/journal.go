//go:build server

package agentrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/tooldecision"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// errAgentRunEnded 表示运行已结束，本次执行尝试停止推理并按失败或取消收尾。
var errAgentRunEnded = errors.New("agent run has ended")

// runJournal 把运行在安全点的过程、用量、任务清单、当前完成与恢复状态写入数据库；每次写入先校验本次执行尝试仍持有运行且运行处于执行中。
type runJournal struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
	policy   agentRunPolicy
	run      *servermodels.AgentRun
}

// NewRunJournal 返回运行的日志，按运行的执行范围选择运行策略。
func NewRunJournal(ctx context.Context, db *bun.DB, enqueuer servertask.TxEnqueuer, run *servermodels.AgentRun) (einorun.Journal, error) {
	policy, err := runPolicy(ctx, db, enqueuer, run)
	if err != nil {
		return nil, err
	}
	return &runJournal{db: db, enqueuer: enqueuer, policy: policy, run: run}, nil
}

// LoadResume 读取运行已保存的恢复状态、过程内容、任务清单与当前完成，运行尚未保存恢复状态时返回空。
func LoadResume(ctx context.Context, db bun.IDB, run *servermodels.AgentRun) (*einorun.Resume, error) {
	return loadResume(ctx, db, run)
}

// SaveStep 在一个事务中写入自上次成功保存以来的过程变化，并更新运行的用量、任务清单、当前完成与恢复状态；调用首次转为待核对时通知负责人。
func (j *runJournal) SaveStep(ctx context.Context, step einorun.Step) error {
	usage, err := json.Marshal(agentcontract.UsageFrom(step.Usage))
	if err != nil {
		return fmt.Errorf("encode agent run usage: %w", err)
	}
	plan, err := encodeRunPlan(step.Plan)
	if err != nil {
		return err
	}
	var completion *string
	if step.Completion != nil {
		encoded, err := json.Marshal(step.Completion)
		if err != nil {
			return fmt.Errorf("encode agent run completion: %w", err)
		}
		completion = new(string(encoded))
	}
	return realtime.RunInTx(ctx, j.db, func(ctx context.Context, tx bun.Tx) error {
		if err := j.lockRunning(ctx, tx); err != nil {
			return err
		}
		changes, err := agentprocess.ApplyChanges(ctx, tx, j.run, step.Changes)
		if err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model((*servermodels.AgentRun)(nil)).
			Set("state = ?", step.State).
			Set("usage = ?::jsonb", string(usage)).
			Set("plan = ?::jsonb", plan).
			Set("completion = ?::jsonb", completion).
			Where("id = ? AND workspace_id = ?", j.run.ID, j.run.WorkspaceID).
			Exec(ctx); err != nil {
			return fmt.Errorf("save agent run state: %w", err)
		}
		return j.notifyReviews(ctx, tx, changes)
	})
}

// SaveToolCall 合并写入一次工具调用：调用首次提交确认或审批、或首次暂停等待确认时在同一事务中指定处理成员并在会话中写入待处理事件，按会话、运行的顺序加锁，
// 找不到处理成员时拒绝提交且不写入；调用首次转为待核对时通知负责人刷新待处理。
func (j *runJournal) SaveToolCall(ctx context.Context, call einorun.ToolCall) error {
	if err := call.Validate(); err != nil {
		return err
	}
	return realtime.RunInTx(ctx, j.db, func(ctx context.Context, tx bun.Tx) error {
		var policyContext agentRunPolicyContext
		submitting := call.Handover == einorun.HandoverSubmitted || agentprocess.Paused(call)
		if submitting {
			var err error
			if policyContext, err = j.policy.lockContext(ctx, tx, j.run); err != nil {
				return err
			}
		}
		if err := j.lockRunning(ctx, tx); err != nil {
			return err
		}
		changes, err := agentprocess.SaveCalls(ctx, tx, j.run, []einorun.ToolCall{call})
		if err != nil {
			return err
		}
		if err := j.notifyReviews(ctx, tx, changes); err != nil {
			return err
		}
		change := changes[0]
		newlyPaused := agentprocess.Paused(change.After) && (change.Before == nil || !agentprocess.Paused(*change.Before))
		if !submitting || !(change.NewlyHandedOver(einorun.HandoverSubmitted) || newlyPaused) {
			return nil
		}
		// 只为本项目工具策略给出的提交与暂停建立待处理项，载荷记下需要的人工介入。
		var submission agentcontract.Submission
		if json.Unmarshal(change.After.Payload, &submission) != nil || submission.Intervention == "" {
			return nil
		}
		err = tooldecision.Submit(ctx, tx, j.enqueuer, policyContext.Conversation, j.run, agentcontract.ToolCall{ID: call.ID, Intervention: submission.Intervention})
		if errors.Is(err, agentcontract.ErrDecisionUnavailable) {
			return einorun.RejectCall(err.Error())
		}
		return err
	})
}

// notifyReviews 在调用首次转为待核对时通知负责人刷新待处理。
func (j *runJournal) notifyReviews(ctx context.Context, tx bun.Tx, changes []agentprocess.CallChange) error {
	if !slices.ContainsFunc(changes, func(change agentprocess.CallChange) bool { return change.NewlySettledAs(einorun.StatusNeedsReview) }) {
		return nil
	}
	return agentprocess.NotifyReviewers(ctx, tx, j.run.WorkspaceID, j.run.ID)
}

// lockRunning 锁定运行并校验本次执行尝试仍持有它：尝试已失效时返回 servertask.ErrExecutionLost，运行已结束时返回 errAgentRunEnded，两者都不写入。
func (j *runJournal) lockRunning(ctx context.Context, tx bun.Tx) error {
	run := &servermodels.AgentRun{}
	if err := tx.NewSelect().Model(run).Column("status", "task_run_id", "task_attempt", "task_instance_id").
		Where("id = ? AND workspace_id = ?", j.run.ID, j.run.WorkspaceID).
		For("UPDATE").Scan(ctx); err != nil {
		return fmt.Errorf("lock agent run for journal: %w", err)
	}
	if err := checkExecution(ctx, run, false); err != nil {
		return err
	}
	if run.Status != string(domain.AgentRunStatusRunning) {
		return errAgentRunEnded
	}
	return nil
}

// loadResume 读取运行已保存的恢复状态、过程内容、任务清单与当前完成，运行尚未保存恢复状态时返回空。
func loadResume(ctx context.Context, db bun.IDB, run *servermodels.AgentRun) (*einorun.Resume, error) {
	saved := &servermodels.AgentRun{}
	if err := db.NewSelect().Model(saved).Column("state", "plan", "completion").
		Where("id = ? AND workspace_id = ?", run.ID, run.WorkspaceID).Scan(ctx); err != nil {
		return nil, fmt.Errorf("load agent run state: %w", err)
	}
	if len(saved.State) == 0 {
		return nil, nil
	}
	blocks, calls, err := agentprocess.LoadRecords(ctx, db, run.WorkspaceID, run.ID)
	if err != nil {
		return nil, err
	}
	resume := &einorun.Resume{State: saved.State, Blocks: blocks, Calls: calls}
	if len(saved.Plan) > 0 {
		if err := json.Unmarshal(saved.Plan, &resume.Plan); err != nil {
			return nil, fmt.Errorf("decode agent run plan: %w", err)
		}
	}
	if len(saved.Completion) > 0 {
		resume.Completion = &einorun.Completion{}
		if err := json.Unmarshal(saved.Completion, resume.Completion); err != nil {
			return nil, fmt.Errorf("decode agent run completion: %w", err)
		}
	}
	return resume, nil
}

// suspend 把执行中的运行改为挂起并推进会话版本；等待的调用已全部有结果时立即投递恢复。
func (a *ExecuteAction) suspend(ctx context.Context, run *servermodels.AgentRun) error {
	return realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		conversation, err := chatstate.LockConversation(ctx, tx, run.WorkspaceID, run.ConversationID)
		if err != nil {
			return err
		}
		updated, err := tx.NewUpdate().Model((*servermodels.AgentRun)(nil)).
			Set("status = ?", domain.AgentRunStatusWaiting).
			Set("task_run_id = NULL, task_attempt = 0, task_instance_id = NULL").
			Where("id = ? AND workspace_id = ? AND status = ?", run.ID, run.WorkspaceID, domain.AgentRunStatusRunning).
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
		return ResumeWaitingRun(ctx, tx, a.enqueuer, run.WorkspaceID, run.ID)
	})
}

// ResumeWaitingRun 在挂起运行等待外部结果的调用全部有结果、暂停等待确认的调用全部有决定时把运行改回排队、推进会话版本并投递服务端任务；
// 运行未挂起或仍有调用未结束时不做任何事。调用方在写入调用结果的 realtime.RunInTx 事务中调用。
func ResumeWaitingRun(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, workspaceID, runID string) error {
	initial := &servermodels.AgentRun{}
	err := db.NewSelect().Model(initial).Where("agr.id = ? AND agr.workspace_id = ?", runID, workspaceID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return fmt.Errorf("load waiting agent run: %w", err)
	}
	if initial.Status != string(domain.AgentRunStatusWaiting) {
		return nil
	}
	// 按会话、运行的顺序加锁。
	conversation, err := chatstate.LockConversation(ctx, db, workspaceID, initial.ConversationID)
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
	// 等待外部结果与暂停等待确认而尚无决定的主调用都有结果后才恢复。
	unsettled, err := db.NewSelect().Model((*servermodels.AgentToolCall)(nil)).
		Where("workspace_id = ? AND agent_run_id = ? AND parent_id IS NULL", workspaceID, runID).
		WhereGroup(" AND ", func(query *bun.SelectQuery) *bun.SelectQuery {
			return query.
				Where("handover = ? AND status IN (?)", einorun.HandoverAwait,
					bun.List([]domain.AgentToolCallStatus{domain.AgentToolCallQueued, domain.AgentToolCallRunning, domain.AgentToolCallWaiting})).
				WhereOr("handover IS NULL AND status = ? AND decision IS NULL AND source <> ?", domain.AgentToolCallAwaitingDecision, domain.AgentToolSourceLocalAgent)
		}).
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("check unsettled agent tool calls: %w", err)
	}
	if unsettled {
		return nil
	}
	if _, err := db.NewUpdate().Model(run).
		Set("status = ?", domain.AgentRunStatusQueued).
		WherePK().Exec(ctx); err != nil {
		return fmt.Errorf("requeue waiting agent run: %w", err)
	}
	if err := chatstate.TouchConversation(ctx, db, conversation, domain.ConversationChangeTimeline); err != nil {
		return err
	}
	// 每次挂起只在改回排队时投递一次，并把新任务登记为执行方；原任务之后的重试与失败收尾按执行方跳过该运行。
	taskRunID, err := enqueuer.EnqueueIn(ctx, RunActionName, RunInput{RunID: runID}, servertask.EnqueueOptions{
		WorkspaceID: workspaceID, Queue: servertask.QueueAgent, MaxAttempts: 3,
	})
	if err != nil {
		return fmt.Errorf("enqueue agent run resume: %w", err)
	}
	if _, err := db.NewUpdate().Model(run).Set("task_run_id = ?, task_attempt = 0, task_instance_id = NULL", taskRunID).WherePK().Exec(ctx); err != nil {
		return fmt.Errorf("register agent run resume task: %w", err)
	}
	return nil
}

//go:build server

package agentrun

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/einorun"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

const (
	// computerWaitWindow 是可挂起的电脑操作在进程内等待结果的时长，超过后运行挂起，结果上报时唤醒。
	computerWaitWindow = 10 * time.Second
	// computerFallbackInterval 是未收到结果写入信号时兜底读取调用记录的间隔，覆盖信号丢失。
	computerFallbackInterval = 5 * time.Second
)

// runComputer 把运行的电脑工具调用作为操作派发到 AI 员工使用的电脑，并等待电脑上报的结果。
type runComputer struct {
	db          *bun.DB
	workspaceID string
	// agentID 是运行的 AI 员工编号，本机 Agent 会话按会话与 AI 员工区分。
	agentID    string
	computerID string
	// folder 是会话默认文件夹在执行器会话文件夹根目录下的名称，取会话编号。
	folder string
	// confined 表示文件操作限定在会话文件夹内，运行服务客户时取值。
	confined bool
	// checkDecided 复核暂停确认后在运行内执行的调用，已不允许时返回交给模型的原因。
	checkDecided func(context.Context, string) (string, error)
}

// Target 返回操作派发到这台电脑时的电脑编号与填入会话默认文件夹和访问范围后的操作。
func (c *runComputer) Target(operation domain.ComputerOperation) agentcontract.ComputerTarget {
	operation.Folder, operation.Confined = c.folder, c.confined
	return agentcontract.ComputerTarget{ComputerID: c.computerID, Operation: operation}
}

// Execute 在电脑在线时把当前工具调用改为派发到电脑并交由电脑推进、记下当前串联编号、等待领取并通知执行器，随后等待结果；电脑离线时直接返回失败原因。
func (c *runComputer) Execute(ctx context.Context, operation domain.ComputerOperation, suspend bool) (domain.ComputerOutcome, error) {
	call, ok := einorun.CallFrom(ctx)
	if !ok {
		return domain.ComputerOutcome{}, errors.New("computer operation requires a recorded tool call")
	}
	callID := call.RecordID
	if failure, err := c.checkDecided(ctx, callID); err != nil || failure != "" {
		return domain.ComputerOutcome{}, cmp.Or(err, errors.New(failure))
	}
	operation = c.Target(operation).Operation
	// 派发前订阅结果写入信号，电脑在订阅生效前上报的结果由等待开始时的首次读取覆盖。
	wake, unwatch := realtime.WatchAgentToolCallSettled(c.workspaceID, callID)
	defer unwatch()
	// 可挂起的调用交由电脑推进，恢复时等待电脑结果；不可挂起的调用在进程内等待，恢复时按进行中的调用结算。
	var handover *einorun.Handover
	if suspend {
		handover = new(einorun.HandoverAwait)
	}
	dispatched := false
	err := realtime.RunInTx(ctx, c.db, func(ctx context.Context, tx bun.Tx) error {
		online, err := tx.NewSelect().Model((*servermodels.Computer)(nil)).
			Where("cmp.id = ? AND cmp.workspace_id = ? AND cmp.revoked_at IS NULL", c.computerID, c.workspaceID).
			Where("cmp.last_seen_at > now() - make_interval(secs => ?)", domain.ComputerPresenceTimeout.Seconds()).
			For("SHARE").Exists(ctx)
		if err != nil {
			return fmt.Errorf("check computer presence: %w", err)
		}
		if !online {
			return nil
		}
		result, err := tx.NewUpdate().Model((*servermodels.AgentToolCall)(nil)).
			Set("computer_id = ?", c.computerID).
			Set("operation = ?", domain.ComputerCall{Operation: operation}).
			Set("status = ?", domain.AgentToolCallQueued).
			Set("handover = ?", handover).
			Set("trace_id = ?", support.NilIfZero(logscope.From(ctx).TraceID)).
			Set("started_at = NULL").
			Where("id = ? AND workspace_id = ? AND status = ?", callID, c.workspaceID, domain.AgentToolCallRunning).
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("dispatch computer operation: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil {
			return fmt.Errorf("dispatch computer operation: %w", err)
		} else if affected == 0 {
			return errors.New("computer operation tool call is not running")
		}
		realtime.Notify(ctx, realtime.ComputerWork(c.workspaceID, c.computerID))
		dispatched = true
		return nil
	})
	if err != nil {
		return domain.ComputerOutcome{}, err
	}
	if !dispatched {
		return domain.ComputerOutcome{}, agentcontract.ErrComputerOffline
	}
	return c.wait(ctx, callID, wake, suspend)
}

// wait 读取调用记录直到电脑上报结果，收到结果写入信号或兜底间隔到期时重新读取；suspend 为 true 时超过等待时长后最后读取一次仍无结果即返回 ErrAwaitExternal，否则一直等到 ctx 结束。
func (c *runComputer) wait(ctx context.Context, callID string, wake <-chan struct{}, suspend bool) (domain.ComputerOutcome, error) {
	fallback := time.NewTicker(computerFallbackInterval)
	defer fallback.Stop()
	var deadline <-chan time.Time
	if suspend {
		timer := time.NewTimer(computerWaitWindow)
		defer timer.Stop()
		deadline = timer.C
	}
	expired := false
	for {
		call := &servermodels.AgentToolCall{}
		if err := c.db.NewSelect().Model(call).
			Column("status", "result", "error", "operation").
			Where("atc.id = ? AND atc.workspace_id = ?", callID, c.workspaceID).
			Scan(ctx); err != nil {
			return domain.ComputerOutcome{}, fmt.Errorf("load computer operation: %w", err)
		}
		switch domain.AgentToolCallStatus(call.Status) {
		case domain.AgentToolCallSucceeded:
			if call.Operation != nil && call.Operation.Outcome != nil {
				return *call.Operation.Outcome, nil
			}
			return domain.ComputerOutcome{Output: support.Deref(call.Result)}, nil
		case domain.AgentToolCallFailed:
			return domain.ComputerOutcome{}, errors.New(support.Deref(call.Error))
		case domain.AgentToolCallInterrupted, domain.AgentToolCallNeedsReview:
			return domain.ComputerOutcome{}, errors.New(support.Deref(call.Result))
		case domain.AgentToolCallCancelled:
			return domain.ComputerOutcome{}, context.Canceled
		}
		if expired {
			return domain.ComputerOutcome{}, agentruntime.ErrAwaitExternal
		}
		select {
		case <-ctx.Done():
			return domain.ComputerOutcome{}, ctx.Err()
		case <-wake:
		case <-fallback.C:
		case <-deadline:
			expired = true
		}
	}
}

// loadRunComputer 读取 AI 员工使用的电脑；电脑已撤销时仍返回记录，派发时按离线处理。
func loadRunComputer(ctx context.Context, db bun.IDB, workspaceID, computerID string) (*servermodels.Computer, error) {
	computer := &servermodels.Computer{}
	if err := db.NewSelect().Model(computer).
		Where("cmp.id = ? AND cmp.workspace_id = ?", computerID, workspaceID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load agent run computer: %w", err)
	}
	return computer, nil
}

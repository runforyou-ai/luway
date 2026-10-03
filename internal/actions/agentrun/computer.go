//go:build server

package agentrun

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

const (
	// computerWaitWindow 是可挂起的电脑操作在进程内等待结果的时长，超过后运行挂起，结果上报时唤醒。
	computerWaitWindow = 10 * time.Second
	// computerPollInterval 是等待电脑结果时读取调用记录的间隔。
	computerPollInterval = 200 * time.Millisecond
)

// runComputer 把运行的电脑工具调用作为操作派发到个人 AI 员工使用的电脑，并等待电脑上报的结果。
type runComputer struct {
	db             *bun.DB
	organizationID string
	computerID     string
	// folder 是会话默认文件夹在执行器会话文件夹根目录下的名称，取会话编号。
	folder string
}

// Execute 在电脑在线时把当前工具调用改为派发到电脑并通知执行器，随后等待结果；电脑离线时直接返回失败原因。
func (c *runComputer) Execute(ctx context.Context, operation domain.ComputerOperation, suspend bool) (domain.ComputerOutcome, error) {
	callID := agentruntime.ToolCallID(ctx)
	if callID == "" {
		return domain.ComputerOutcome{}, errors.New("computer operation requires a recorded tool call")
	}
	operation.Folder = c.folder
	dispatched := false
	err := realtime.RunInTx(ctx, c.db, func(ctx context.Context, tx bun.Tx) error {
		online, err := tx.NewSelect().Model((*servermodels.Computer)(nil)).
			Where("cmp.id = ? AND cmp.organization_id = ? AND cmp.revoked_at IS NULL", c.computerID, c.organizationID).
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
			Set("status = ?", domain.AgentToolCallWaiting).
			Set("updated_at = now()").
			Where("id = ? AND organization_id = ? AND status = ?", callID, c.organizationID, domain.AgentToolCallRunning).
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("dispatch computer operation: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil {
			return fmt.Errorf("dispatch computer operation: %w", err)
		} else if affected == 0 {
			return errors.New("computer operation tool call is not running")
		}
		realtime.Notify(ctx, realtime.ComputerWork(c.organizationID, c.computerID))
		dispatched = true
		return nil
	})
	if err != nil {
		return domain.ComputerOutcome{}, err
	}
	if !dispatched {
		return domain.ComputerOutcome{}, agentruntime.ErrComputerOffline
	}
	return c.wait(ctx, callID, suspend)
}

// wait 按间隔读取调用记录直到电脑上报结果；suspend 为 true 时超过等待时长返回 ErrAwaitExternal，否则一直等到 ctx 结束。
func (c *runComputer) wait(ctx context.Context, callID string, suspend bool) (domain.ComputerOutcome, error) {
	deadline := time.Now().Add(computerWaitWindow)
	ticker := time.NewTicker(computerPollInterval)
	defer ticker.Stop()
	for {
		call := &servermodels.AgentToolCall{}
		if err := c.db.NewSelect().Model(call).
			Column("status", "result", "error", "operation").
			Where("atc.id = ? AND atc.organization_id = ?", callID, c.organizationID).
			Scan(ctx); err != nil {
			return domain.ComputerOutcome{}, fmt.Errorf("load computer operation: %w", err)
		}
		switch domain.AgentToolCallStatus(call.Status) {
		case domain.AgentToolCallSucceeded:
			if call.Operation != nil && call.Operation.Outcome != nil {
				return *call.Operation.Outcome, nil
			}
			return domain.ComputerOutcome{Output: common.StringValue(call.Result)}, nil
		case domain.AgentToolCallFailed:
			return domain.ComputerOutcome{}, errors.New(common.StringValue(call.Error))
		case domain.AgentToolCallInterrupted, domain.AgentToolCallNeedsReview:
			return domain.ComputerOutcome{}, errors.New(common.StringValue(call.Result))
		case domain.AgentToolCallCancelled:
			return domain.ComputerOutcome{}, context.Canceled
		}
		if suspend && time.Now().After(deadline) {
			return domain.ComputerOutcome{}, agentruntime.ErrAwaitExternal
		}
		select {
		case <-ctx.Done():
			return domain.ComputerOutcome{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// loadRunComputer 读取个人 AI 员工使用的电脑；电脑已撤销时仍返回记录，派发时按离线处理。
func loadRunComputer(ctx context.Context, db bun.IDB, organizationID, computerID string) (*servermodels.Computer, error) {
	computer := &servermodels.Computer{}
	if err := db.NewSelect().Model(computer).
		Where("cmp.id = ? AND cmp.organization_id = ?", computerID, organizationID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load agent run computer: %w", err)
	}
	return computer, nil
}

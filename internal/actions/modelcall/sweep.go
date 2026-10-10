//go:build server

package modelcall

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

const (
	// SweepInterruptedActionName 是结束中断模型调用的定时 Action 名称。
	SweepInterruptedActionName = "modelcall.sweep_interrupted"
	// SweepInterruptedScheduleKey 是结束中断模型调用的定时计划标识。
	SweepInterruptedScheduleKey = "model-call-interrupted-sweep"
	// interruptedAfter 是调用开始后仍未结束即视为中断的时长。
	interruptedAfter = time.Hour
	// interruptedMessage 是中断调用记录的失败原因。
	interruptedMessage = "model call interrupted before completion"
	// sweepBatchSize 是每次结束的中断调用上限。
	sweepBatchSize = 100
)

// SweepInterruptedAction 结束进程中断或结果写入失败而一直处于进行中的模型调用。
type SweepInterruptedAction struct{ invoker *Invoker }

// NewSweepInterruptedAction 创建中断模型调用的结束操作。
func NewSweepInterruptedAction(invoker *Invoker) *SweepInterruptedAction {
	return &SweepInterruptedAction{invoker: invoker}
}

// Execute 把开始超过一小时仍在进行中的调用记为失败、其进行中的上游尝试记为已取消，在同一事务内按已记录的上游事实执行结束回调。
func (a *SweepInterruptedAction) Execute(ctx context.Context, _ struct{}) error {
	var ids []string
	if err := a.invoker.db.NewSelect().Model((*servermodels.AIModelCall)(nil)).
		ColumnExpr("amc.id::text").
		Where("amc.status = ?", domain.AIModelCallStatusRunning).
		Where("amc.created_at < now() - make_interval(secs => ?)", interruptedAfter.Seconds()).
		OrderExpr("amc.id ASC").
		Limit(sweepBatchSize).
		Scan(ctx, &ids); err != nil {
		return fmt.Errorf("list interrupted AI model calls: %w", err)
	}
	for _, id := range ids {
		if err := serverstorage.RunInTx(ctx, a.invoker.db, func(ctx context.Context, tx bun.Tx) error {
			return a.invoker.finishInterrupted(ctx, tx, id)
		}); err != nil {
			return fmt.Errorf("finish interrupted AI model call %s: %w", id, err)
		}
	}
	if len(ids) > 0 {
		slog.InfoContext(ctx, "已结束中断的模型调用", "count", len(ids))
	}
	return nil
}

// finishInterrupted 在调用行锁内把进行中的上游尝试记为已取消，汇总上游事实并结束中断调用。
func (i *Invoker) finishInterrupted(ctx context.Context, tx bun.Tx, callID string) error {
	record, err := lockRunning(ctx, tx, callID)
	if err != nil || record == nil {
		return err
	}
	var succeeded bool
	if err := tx.NewSelect().Model((*servermodels.AIModelCallAttempt)(nil)).
		ColumnExpr("COALESCE(bool_or(status = ?), false)", domain.AIModelCallStatusSucceeded).
		Where("call_id = ?", callID).Scan(ctx, &succeeded); err != nil {
		return err
	}
	if _, err := tx.NewUpdate().Model((*servermodels.AIModelCallAttempt)(nil)).
		Set("status = ?", domain.AIModelCallStatusCanceled).
		Set("error_message = ?", interruptedMessage).
		Set("finished_at = now()").
		Where("call_id = ? AND status = ?", callID, domain.AIModelCallStatusRunning).
		Exec(ctx); err != nil {
		return err
	}
	var usage Usage
	if err := tx.NewSelect().Model((*servermodels.AIModelCallAttempt)(nil)).
		ColumnExpr("COALESCE(sum(input_tokens), 0), COALESCE(sum(cached_input_tokens), 0), COALESCE(sum(output_tokens), 0)").
		Where("call_id = ?", callID).
		Scan(ctx, &usage.InputTokens, &usage.CachedInputTokens, &usage.OutputTokens); err != nil {
		return err
	}
	return i.finishLocked(ctx, tx, record, usage, domain.AIModelCallStatusFailed, interruptedMessage, succeeded)
}

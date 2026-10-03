//go:build server

package modelcall

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/credit"
	"github.com/runforyou-ai/luway/internal/domain"
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
type SweepInterruptedAction struct{ db *bun.DB }

// NewSweepInterruptedAction 创建中断模型调用的结束操作。
func NewSweepInterruptedAction(db *bun.DB) *SweepInterruptedAction {
	return &SweepInterruptedAction{db: db}
}

// Execute 把开始超过一小时仍在进行中的调用及其上游尝试记为失败；平台模型按已记录的上游用量结算积分，没有用量时退回全部预占。
func (a *SweepInterruptedAction) Execute(ctx context.Context, _ struct{}) error {
	var ids []string
	if err := a.db.NewSelect().Model((*servermodels.AIModelCall)(nil)).
		ColumnExpr("amc.id::text").
		Where("amc.status = ?", domain.AIModelCallStatusRunning).
		Where("amc.created_at < ?", time.Now().Add(-interruptedAfter)).
		OrderExpr("amc.id ASC").
		Limit(sweepBatchSize).
		Scan(ctx, &ids); err != nil {
		return fmt.Errorf("list interrupted AI model calls: %w", err)
	}
	for _, id := range ids {
		if err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			return finishInterrupted(ctx, tx, id)
		}); err != nil {
			return fmt.Errorf("finish interrupted AI model call %s: %w", id, err)
		}
	}
	return nil
}

// finishInterrupted 锁定仍在进行中的调用，把进行中的上游尝试与调用记为失败，用量取各尝试已记录的用量之和，平台模型按调用时的价格结算积分。
func finishInterrupted(ctx context.Context, tx bun.Tx, callID string) error {
	call := &servermodels.AIModelCall{}
	err := tx.NewSelect().Model(call).
		Where("amc.id = ? AND amc.status = ?", callID, domain.AIModelCallStatusRunning).
		For("UPDATE").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.NewUpdate().Model((*servermodels.AIModelCallAttempt)(nil)).
		Set("status = ?", domain.AIModelCallStatusFailed).
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
	var settlement credit.Settlement
	if call.InputCreditPrice != nil {
		var cost int64
		if usage != (Usage{}) {
			cost = domain.CreditPrice{Input: *call.InputCreditPrice, Output: *call.OutputCreditPrice, Request: *call.RequestCreditPrice}.
				Cost(usage.InputTokens, usage.OutputTokens)
		}
		if settlement, err = credit.Settle(ctx, tx, call.OrganizationID, callID, cost, time.Now()); err != nil {
			return err
		}
	}
	_, err = tx.NewUpdate().Model(call).
		Set("status = ?", domain.AIModelCallStatusFailed).
		Set("input_tokens = ?", usage.InputTokens).
		Set("cached_input_tokens = ?", usage.CachedInputTokens).
		Set("output_tokens = ?", usage.OutputTokens).
		Set("error_message = ?", interruptedMessage).
		Set("credits = ?", settlement.Charged).
		Set("credit_shortfall = ?", settlement.Shortfall).
		Set("finished_at = now()").
		WherePK().
		Exec(ctx)
	return err
}

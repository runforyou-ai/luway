//go:build server

package commerce

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/credit"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/commerce"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

const (
	// SyncChangesActionName 是读取商业服务变更源的任务 Action 名称。
	SyncChangesActionName = "commerce.sync_changes"
	// SyncChangesScheduleKey 是定时读取商业服务变更源的计划标识。
	SyncChangesScheduleKey = "commerce-changes"
	// changePageSize 是每次读取变更源的条数。
	changePageSize = 200
)

// SyncChangesEnqueueOptions 是投递读取变更源任务的选项：同一时刻最多一个待执行的读取。
var SyncChangesEnqueueOptions = servertask.EnqueueOptions{Queue: "maintenance", MaxAttempts: 3, IdempotencyKey: "commerce-changes"}

// SyncChangesInput 是读取商业服务变更源任务的输入。
type SyncChangesInput struct{}

// SyncChangesAction 读取商业服务变更源，按序应用工作区权益与积分充值订单并推进已应用序号。
type SyncChangesAction struct {
	db     *bun.DB
	client *commerce.Client
}

// NewSyncChangesAction 创建读取变更源操作。
func NewSyncChangesAction(db *bun.DB, client *commerce.Client) *SyncChangesAction {
	return &SyncChangesAction{db: db, client: client}
}

// Execute 是读取变更源的后台任务：未配对时直接完成，读取失败时记录失败原因并返回错误。
func (a *SyncChangesAction) Execute(ctx context.Context, _ SyncChangesInput) error {
	err := a.Sync(ctx)
	if errors.Is(err, ErrNotPaired) {
		return nil
	}
	return err
}

// Sync 从已应用序号之后逐页读取变更源直至读完，每页在一个事务内应用并推进序号；读取或应用失败时在配对上记录失败原因，配对在读取期间被替换或其他读取已推进序号时停止。
func (a *SyncChangesAction) Sync(ctx context.Context) error {
	for {
		pairing, err := loadPairing(ctx, a.db.NewSelect())
		if err != nil {
			return err
		}
		more, err := a.syncPage(ctx, pairing)
		if err != nil {
			return a.recordFailure(ctx, pairing, err)
		}
		if !more {
			return nil
		}
	}
}

// syncPage 读取并应用配对已应用序号之后的一页变更，返回是否还需读取下一页。
func (a *SyncChangesAction) syncPage(ctx context.Context, pairing *servermodels.CommercePairing) (bool, error) {
	changes, err := a.client.Changes(ctx, pairing.URL, pairing.ChangeSequence, changePageSize)
	if err != nil {
		return false, clientError(err)
	}
	advanced := false
	err = a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		locked := &servermodels.CommercePairing{}
		if err := tx.NewSelect().Model(locked).
			Where("cp.server_id = ? AND cp.created_at = ? AND cp.change_sequence = ?", pairing.ServerID, pairing.CreatedAt, pairing.ChangeSequence).
			For("UPDATE").
			Scan(ctx); errors.Is(err, sql.ErrNoRows) {
			// 配对已替换或其他读取已推进序号。
			return nil
		} else if err != nil {
			return err
		}
		now := time.Now()
		sequence := pairing.ChangeSequence
		for _, change := range changes {
			if change.Sequence <= sequence {
				return fmt.Errorf("%w: change sequence %d is not after %d", ErrInvalidData, change.Sequence, sequence)
			}
			if err := apply(ctx, tx, change, now); err != nil {
				return fmt.Errorf("apply commerce change %d: %w", change.Sequence, err)
			}
			sequence = change.Sequence
		}
		advanced = true
		_, err := tx.NewUpdate().Model(locked).
			Set("change_sequence = ?", sequence).
			Set("synced_at = now()").Set("failed_at = NULL").Set("failure = ''").
			WherePK().
			Exec(ctx)
		return err
	})
	return advanced && len(changes) == changePageSize, err
}

// recordFailure 在配对仍为本次读取的配对且序号未被推进时记录失败时间与原因，返回原错误；请求已取消时不记录。
func (a *SyncChangesAction) recordFailure(ctx context.Context, pairing *servermodels.CommercePairing, syncErr error) error {
	if ctx.Err() != nil {
		return syncErr
	}
	failure := domain.CommerceSyncFailureFailed
	switch {
	case errors.Is(syncErr, ErrUnavailable):
		failure = domain.CommerceSyncFailureUnavailable
	case errors.Is(syncErr, ErrNotPaired):
		failure = domain.CommerceSyncFailureNotPaired
	case errors.Is(syncErr, ErrInvalidData):
		failure = domain.CommerceSyncFailureInvalidData
	}
	slog.Warn("读取商业服务变更源失败", "sequence", pairing.ChangeSequence, "failure", failure, "error", syncErr)
	if _, err := a.db.NewUpdate().Model(pairing).
		Set("failed_at = now()").Set("failure = ?", failure).
		WherePK().Where("created_at = ? AND change_sequence = ?", pairing.CreatedAt, pairing.ChangeSequence).
		Exec(ctx); err != nil {
		return errors.Join(syncErr, fmt.Errorf("record commerce sync failure: %w", err))
	}
	return syncErr
}

// apply 在事务内应用一条变更：类型未知时跳过；已知类型缺少状态、取值不符合约定、同号订单已按其他工作区或积分入账，或退款找不到充值批次时返回 ErrInvalidData；工作区已删除时跳过。
func apply(ctx context.Context, tx bun.Tx, change commerce.Change, now time.Time) error {
	entitlement, order := change.Entitlement, change.CreditOrder
	// 校验已知类型的变更内容。
	switch change.Type {
	case commerce.ChangeEntitlement:
		if entitlement == nil || entitlement.Revision <= 0 || entitlement.Plan.ID == "" || entitlement.SeatLimit < 0 {
			return fmt.Errorf("%w: malformed entitlement", ErrInvalidData)
		}
	case commerce.ChangeCreditOrder:
		if order == nil || order.OrderID == "" ||
			!(order.Status == commerce.CreditOrderPaid && order.Credits > 0 && order.Credits <= domain.MaxCreditAmount || order.Status == commerce.CreditOrderRefunded) {
			return fmt.Errorf("%w: malformed credit order", ErrInvalidData)
		}
	default:
		return nil
	}
	if !common.ValidUUID(change.WorkspaceID) {
		return fmt.Errorf("%w: malformed workspace id", ErrInvalidData)
	}
	exists, err := tx.NewSelect().Model((*servermodels.Organization)(nil)).Where("o.id = ?", change.WorkspaceID).Exists(ctx)
	if err != nil {
		return err
	}
	if !exists {
		slog.Info("商业服务变更的工作区已删除，已跳过", "sequence", change.Sequence, "workspace_id", change.WorkspaceID)
		return nil
	}
	switch {
	case change.Type == commerce.ChangeEntitlement:
		_, err := tx.NewInsert().Model(&servermodels.WorkspaceEntitlement{
			OrganizationID: change.WorkspaceID, Revision: entitlement.Revision, PlanID: entitlement.Plan.ID,
			PlanName: entitlement.Plan.Name, SeatLimit: entitlement.SeatLimit, PeriodEnd: entitlement.PeriodEnd,
		}).
			Column("organization_id", "revision", "plan_id", "plan_name", "seat_limit", "period_end").
			On("CONFLICT (organization_id) DO UPDATE").
			Set("revision = EXCLUDED.revision, plan_id = EXCLUDED.plan_id, plan_name = EXCLUDED.plan_name").
			Set("seat_limit = EXCLUDED.seat_limit, period_end = EXCLUDED.period_end, updated_at = now()").
			Where("we.revision < EXCLUDED.revision").
			Exec(ctx)
		return err
	case order.Status == commerce.CreditOrderPaid:
		err := credit.Purchase(ctx, tx, change.WorkspaceID, order.OrderID, order.Credits)
		if errors.Is(err, credit.ErrPurchaseConflict) {
			return fmt.Errorf("%w: order %s conflicts with an earlier payment", ErrInvalidData, order.OrderID)
		}
		return err
	default:
		err := credit.Refund(ctx, tx, change.WorkspaceID, order.OrderID, now)
		if errors.Is(err, credit.ErrPurchaseNotFound) {
			return fmt.Errorf("%w: refunded order %s was never paid", ErrInvalidData, order.OrderID)
		}
		return err
	}
}

//go:build server

package credit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

var (
	// ErrPurchaseNotFound 表示工作区没有该充值订单入账的批次。
	ErrPurchaseNotFound = errors.New("purchased credits not found")
	// ErrPurchaseConflict 表示同号充值订单已按其他工作区或积分入账。
	ErrPurchaseConflict = errors.New("purchased credits conflict")
)

// Purchase 在事务内按订单号幂等入账一批不过期的充值积分；订单已按相同工作区与积分入账时不变动，已按其他工作区或积分入账时返回 ErrPurchaseConflict。
func Purchase(ctx context.Context, tx bun.Tx, organizationID, orderID string, amount int64) error {
	result, err := tx.NewInsert().Model(&servermodels.CreditLot{
		OrganizationID: organizationID, Source: string(domain.CreditLotSourcePurchase), OrderID: &orderID,
		Amount: amount, Remaining: amount,
	}).Column("organization_id", "source", "order_id", "amount", "remaining").
		On("CONFLICT (order_id) DO NOTHING").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("record purchased credits: %w", err)
	}
	if inserted, err := result.RowsAffected(); err != nil || inserted == 1 {
		return err
	}
	existing := &servermodels.CreditLot{}
	if err := tx.NewSelect().Model(existing).Column("organization_id", "amount").Where("cl.order_id = ?", orderID).Scan(ctx); err != nil {
		return fmt.Errorf("read purchased credits: %w", err)
	}
	if existing.OrganizationID != organizationID || existing.Amount != amount {
		return ErrPurchaseConflict
	}
	return nil
}

// Refund 在事务内扣回工作区充值订单批次的剩余积分，并让批次在退款时间过期；已退款时不变动，订单未入账时返回 ErrPurchaseNotFound。
func Refund(ctx context.Context, tx bun.Tx, organizationID, orderID string, now time.Time) error {
	lot := &servermodels.CreditLot{}
	err := tx.NewSelect().Model(lot).
		Where("cl.organization_id = ? AND cl.order_id = ?", organizationID, orderID).
		For("UPDATE").
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPurchaseNotFound
	}
	if err != nil {
		return fmt.Errorf("lock purchased credits: %w", err)
	}
	if lot.ExpiresAt != nil {
		return nil
	}
	if lot.Remaining > 0 {
		if err := move(ctx, tx, lot, -lot.Remaining, source{kind: domain.CreditMovementSourceRefund, id: lot.ID}); err != nil {
			return err
		}
	}
	if _, err := tx.NewUpdate().Model(lot).Set("expires_at = ?", now).WherePK().Exec(ctx); err != nil {
		return fmt.Errorf("close refunded credits: %w", err)
	}
	return nil
}

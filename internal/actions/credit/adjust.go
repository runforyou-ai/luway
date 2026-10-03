//go:build server

package credit

import (
	"context"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// Adjust 在事务内记录平台管理员对工作区积分的调整并返回实际变动积分：增加时入账一个不过期的批次；扣减时按扣减顺序从可用批次扣减，最多扣到余额为 0，余额为 0 时返回 ErrInsufficient。
func Adjust(ctx context.Context, tx bun.Tx, organizationID, accountID string, amount int64, note string, now time.Time) (int64, error) {
	if amount < 0 {
		if err := prepareDailyGrant(ctx, tx, organizationID, now); err != nil {
			return 0, err
		}
		lots, err := lockLots(ctx, tx, organizationID, nil, now)
		if err != nil {
			return 0, err
		}
		amount = -min(-amount, available(lots, now))
		if amount == 0 {
			return 0, ErrInsufficient
		}
		adjustment, err := insertAdjustment(ctx, tx, organizationID, accountID, amount, note)
		if err != nil {
			return 0, err
		}
		if _, err := debit(ctx, tx, lots, -amount, source{kind: domain.CreditMovementSourceAdjustment, id: adjustment.ID}, now); err != nil {
			return 0, err
		}
		return amount, nil
	}
	adjustment, err := insertAdjustment(ctx, tx, organizationID, accountID, amount, note)
	if err != nil {
		return 0, err
	}
	if _, err := tx.NewInsert().Model(&servermodels.CreditLot{
		OrganizationID: organizationID, Source: string(domain.CreditLotSourceAdjustment), AdjustmentID: &adjustment.ID,
		Amount: amount, Remaining: amount,
	}).Column("organization_id", "source", "adjustment_id", "amount", "remaining").Exec(ctx); err != nil {
		return 0, fmt.Errorf("record credit lot: %w", err)
	}
	return amount, nil
}

// insertAdjustment 写入一条积分调整记录。
func insertAdjustment(ctx context.Context, tx bun.Tx, organizationID, accountID string, amount int64, note string) (*servermodels.CreditAdjustment, error) {
	adjustment := &servermodels.CreditAdjustment{OrganizationID: organizationID, AccountID: accountID, Amount: amount, Note: note}
	if _, err := tx.NewInsert().Model(adjustment).
		Column("organization_id", "account_id", "amount", "note").
		Returning("id").
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("record credit adjustment: %w", err)
	}
	return adjustment, nil
}

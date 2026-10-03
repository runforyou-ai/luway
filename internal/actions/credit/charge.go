//go:build server

package credit

import (
	"context"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// Reserve 在事务内为模型调用预占积分：先发放今天的每日赠送，再按扣减顺序从可用批次扣减；可用积分不足时返回 ErrInsufficient。
func Reserve(ctx context.Context, tx bun.Tx, organizationID, callID string, amount int64, now time.Time) error {
	if amount <= 0 {
		return nil
	}
	if err := prepareDailyGrant(ctx, tx, organizationID, now); err != nil {
		return err
	}
	from := source{kind: domain.CreditMovementSourceModelCall, id: callID}
	lots, err := lockLots(ctx, tx, organizationID, nil, now)
	if err != nil {
		return err
	}
	if available(lots, now) < amount {
		return ErrInsufficient
	}
	_, err = debit(ctx, tx, lots, amount, from, now)
	return err
}

// Settlement 定义模型调用结算后的实际扣除积分与余额不足未能补扣的积分。
type Settlement struct {
	Charged   int64
	Shortfall int64
}

// Settle 在事务内按实际费用结算模型调用：预占多于费用时把差额退回预占的批次，少于费用时从可用批次补扣，最多扣到余额为 0，不足部分记为差额。
func Settle(ctx context.Context, tx bun.Tx, organizationID, callID string, cost int64, now time.Time) (Settlement, error) {
	from := source{kind: domain.CreditMovementSourceModelCall, id: callID}
	if err := prepareDailyGrant(ctx, tx, organizationID, now); err != nil {
		return Settlement{}, err
	}
	lots, err := lockLots(ctx, tx, organizationID, &from, now)
	if err != nil {
		return Settlement{}, err
	}
	charged, err := chargedByLot(ctx, tx, from)
	if err != nil {
		return Settlement{}, err
	}
	reserved := sumCharged(charged)
	if cost <= reserved {
		if err := refund(ctx, tx, lots, charged, reserved-cost, from); err != nil {
			return Settlement{}, err
		}
		return Settlement{Charged: cost}, nil
	}
	debited, err := debit(ctx, tx, lots, cost-reserved, from, now)
	if err != nil {
		return Settlement{}, err
	}
	return Settlement{Charged: reserved + debited, Shortfall: cost - reserved - debited}, nil
}

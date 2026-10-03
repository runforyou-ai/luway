//go:build server

// Package credit 维护工作区积分账本：按需发放每日赠送，模型调用预占与结算，平台管理员调整，以及余额与流水。
package credit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ErrInsufficient 表示工作区可用积分不足以支付本次预占或扣减。
var ErrInsufficient = errors.New("insufficient credits")

// source 定义一次批次变动的业务来源。
type source struct {
	kind domain.CreditMovementSource
	id   string
}

// prepareDailyGrant 在事务内按平台时区发放工作区今天的每日赠送：每日赠送积分为 0、今天已发放或已发放过更晚日期时不发放，赠送在平台时区当天结束时过期。
func prepareDailyGrant(ctx context.Context, tx bun.Tx, organizationID string, now time.Time) error {
	platform := &servermodels.Platform{}
	err := tx.NewSelect().Model(platform).Column("time_zone", "daily_credit_grant").Limit(1).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) || err == nil && platform.DailyCreditGrant <= 0 {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read daily credit grant: %w", err)
	}
	location, err := time.LoadLocation(platform.TimeZone)
	if err != nil {
		location = time.UTC
	}
	local := now.In(location)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	date := day.Format(time.DateOnly)
	if _, err := tx.NewRaw(`
		INSERT INTO credit_lots (organization_id, source, grant_date, amount, remaining, expires_at)
		SELECT ?, ?, ?::date, ?, ?, ?
		WHERE NOT EXISTS (SELECT 1 FROM credit_lots WHERE organization_id = ? AND grant_date >= ?::date)
		ON CONFLICT (organization_id, grant_date) DO NOTHING`,
		organizationID, domain.CreditLotSourceDailyGrant, date, platform.DailyCreditGrant, platform.DailyCreditGrant, day.AddDate(0, 0, 1),
		organizationID, date,
	).Exec(ctx); err != nil {
		return fmt.Errorf("grant daily credits: %w", err)
	}
	return nil
}

// lockLots 按编号顺序以 UPDATE 锁定工作区未过期且有剩余的批次，以及 from 不为空时其变动过的批次，返回按扣减顺序排列的结果：先过期的在前，不过期的在后。
func lockLots(ctx context.Context, tx bun.Tx, organizationID string, from *source, now time.Time) ([]*servermodels.CreditLot, error) {
	lots := make([]*servermodels.CreditLot, 0)
	if err := tx.NewSelect().Model(&lots).
		Where("cl.organization_id = ?", organizationID).
		WhereGroup(" AND ", func(query *bun.SelectQuery) *bun.SelectQuery {
			query = query.Where("cl.remaining > 0 AND (cl.expires_at IS NULL OR cl.expires_at > ?)", now)
			if from != nil {
				query = query.WhereOr("cl.id IN (SELECT cm.lot_id FROM credit_movements AS cm WHERE cm.source_type = ? AND cm.source_id = ?)", from.kind, from.id)
			}
			return query
		}).
		OrderExpr("cl.id ASC").
		For("UPDATE").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("lock credit lots: %w", err)
	}
	slices.SortStableFunc(lots, func(a, b *servermodels.CreditLot) int {
		switch {
		case a.ExpiresAt == nil && b.ExpiresAt == nil:
			return 0
		case a.ExpiresAt == nil:
			return 1
		case b.ExpiresAt == nil:
			return -1
		default:
			return a.ExpiresAt.Compare(*b.ExpiresAt)
		}
	})
	return lots, nil
}

// available 返回批次中此刻可用的剩余积分之和。
func available(lots []*servermodels.CreditLot, now time.Time) int64 {
	var total int64
	for _, lot := range lots {
		if lot.ExpiresAt == nil || lot.ExpiresAt.After(now) {
			total += lot.Remaining
		}
	}
	return total
}

// debit 按扣减顺序从可用批次扣减最多 amount 积分并写入变动，返回实际扣减的积分。
func debit(ctx context.Context, tx bun.Tx, lots []*servermodels.CreditLot, amount int64, from source, now time.Time) (int64, error) {
	var debited int64
	for _, lot := range lots {
		if debited == amount {
			break
		}
		if lot.Remaining <= 0 || lot.ExpiresAt != nil && !lot.ExpiresAt.After(now) {
			continue
		}
		take := min(lot.Remaining, amount-debited)
		if err := move(ctx, tx, lot, -take, from); err != nil {
			return 0, err
		}
		debited += take
	}
	return debited, nil
}

// refund 按扣减顺序的逆序把最多 amount 积分退回 from 在各批次的净扣减 charged 中并写入变动；退回已过期批次的积分随批次作废。
func refund(ctx context.Context, tx bun.Tx, lots []*servermodels.CreditLot, charged map[string]int64, amount int64, from source) error {
	for _, lot := range slices.Backward(lots) {
		if amount == 0 {
			break
		}
		give := min(charged[lot.ID], amount)
		if give <= 0 {
			continue
		}
		if err := move(ctx, tx, lot, give, from); err != nil {
			return err
		}
		amount -= give
	}
	return nil
}

// chargedByLot 返回 from 在各批次上的净扣减积分。
func chargedByLot(ctx context.Context, tx bun.Tx, from source) (map[string]int64, error) {
	var rows []struct {
		LotID   string `bun:"lot_id"`
		Charged int64  `bun:"charged"`
	}
	if err := tx.NewSelect().Model((*servermodels.CreditMovement)(nil)).
		ColumnExpr("cm.lot_id::text AS lot_id, -sum(cm.amount) AS charged").
		Where("cm.source_type = ? AND cm.source_id = ?", from.kind, from.id).
		GroupExpr("cm.lot_id").
		Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("read credit movements: %w", err)
	}
	charged := make(map[string]int64, len(rows))
	for _, row := range rows {
		charged[row.LotID] = row.Charged
	}
	return charged, nil
}

// move 修改批次剩余积分并追加对应变动。
func move(ctx context.Context, tx bun.Tx, lot *servermodels.CreditLot, amount int64, from source) error {
	if _, err := tx.NewUpdate().Model(lot).Set("remaining = remaining + ?", amount).WherePK().Exec(ctx); err != nil {
		return fmt.Errorf("update credit lot: %w", err)
	}
	lot.Remaining += amount
	if _, err := tx.NewInsert().Model(&servermodels.CreditMovement{
		OrganizationID: lot.OrganizationID, LotID: lot.ID, Amount: amount, SourceType: string(from.kind), SourceID: from.id,
	}).Column("organization_id", "lot_id", "amount", "source_type", "source_id").Exec(ctx); err != nil {
		return fmt.Errorf("record credit movement: %w", err)
	}
	return nil
}

// sumCharged 返回 from 的净扣减积分。
func sumCharged(charged map[string]int64) int64 {
	var total int64
	for _, amount := range charged {
		total += amount
	}
	return total
}

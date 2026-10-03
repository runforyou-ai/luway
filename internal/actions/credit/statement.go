//go:build server

package credit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// ErrPageInvalid 表示流水分页参数无效。
var ErrPageInvalid = errors.New("credit entry page invalid")

// Balance 定义工作区当前可用积分与今天的每日赠送。
type Balance struct {
	Available           int64
	DailyGrant          int64      // 平台当前设置的每日赠送积分。
	DailyGrantRemaining int64      // 今天的每日赠送尚未用完的积分。
	DailyGrantExpiresAt *time.Time // 今天的每日赠送过期时间，今天没有发放时为空。
}

// GetBalance 先发放工作区今天的每日赠送，再返回可用积分与今天的每日赠送。
func GetBalance(ctx context.Context, db *bun.DB, organizationID string) (Balance, error) {
	now := time.Now()
	var balance Balance
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := prepareDailyGrant(ctx, tx, organizationID, now); err != nil {
			return err
		}
		if err := tx.NewRaw(`
			SELECT
				(SELECT COALESCE(sum(remaining), 0) FROM credit_lots
					WHERE organization_id = ? AND (expires_at IS NULL OR expires_at > ?)) AS available,
				COALESCE((SELECT daily_credit_grant FROM platforms LIMIT 1), 0) AS daily_grant`,
			organizationID, now,
		).Scan(ctx, &balance.Available, &balance.DailyGrant); err != nil {
			return fmt.Errorf("read credit balance: %w", err)
		}
		var expiresAt time.Time
		err := tx.NewRaw(`
			SELECT remaining, expires_at FROM credit_lots
			WHERE organization_id = ? AND source = ? AND expires_at > ?
			ORDER BY grant_date DESC LIMIT 1`,
			organizationID, domain.CreditLotSourceDailyGrant, now,
		).Scan(ctx, &balance.DailyGrantRemaining, &expiresAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read daily credit grant: %w", err)
		}
		balance.DailyGrantExpiresAt = &expiresAt
		return nil
	})
	if err != nil {
		return Balance{}, err
	}
	return balance, nil
}

// Entry 定义工作区积分流水中的一个业务事件：入账为正数，扣除为负数；模型调用一次一条，进行中时为当前预占积分。
type Entry struct {
	ID           string                   `bun:"id"`
	Kind         domain.CreditEntryKind   `bun:"kind"`
	OccurredAt   time.Time                `bun:"occurred_at"`
	Amount       int64                    `bun:"amount"`
	Note         string                   `bun:"note"`
	ModelName    string                   `bun:"model_name"`
	ModelUsage   domain.AIModelUsage      `bun:"model_usage"`
	CallStatus   domain.AIModelCallStatus `bun:"call_status"`
	InputTokens  int64                    `bun:"input_tokens"`
	OutputTokens int64                    `bun:"output_tokens"`
}

// EntryList 定义积分流水分页结果。
type EntryList struct {
	Entries []Entry
	Page    common.PageInfo
}

// ListEntries 先发放工作区今天的每日赠送，再按发生时间倒序返回工作区积分流水，同时发生的扣除排在入账之前：每日赠送、平台管理员调整、平台模型调用、充值、充值退款与批次过期。
func ListEntries(ctx context.Context, db *bun.DB, organizationID string, page, pageSize int) (EntryList, error) {
	page, pageSize, valid := common.NormalizePagination(page, pageSize)
	if !valid {
		return EntryList{}, ErrPageInvalid
	}
	now := time.Now()
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return prepareDailyGrant(ctx, tx, organizationID, now)
	}); err != nil {
		return EntryList{}, err
	}
	entries := db.NewRaw(`
		SELECT cl.id::text AS id, ? AS kind, cl.created_at AS occurred_at, cl.amount, '' AS note,
			'' AS model_name, '' AS model_usage, '' AS call_status, 0 AS input_tokens, 0 AS output_tokens
		FROM credit_lots AS cl WHERE cl.organization_id = ? AND cl.source = ?
		UNION ALL
		SELECT ca.id::text, ?, ca.created_at, ca.amount, ca.note, '', '', '', 0, 0
		FROM credit_adjustments AS ca WHERE ca.organization_id = ?
		UNION ALL
		SELECT amc.id::text, ?, amc.created_at, -amc.credits, '', amc.model_name, amc.model_usage, amc.status, amc.input_tokens, amc.output_tokens
		FROM ai_model_calls AS amc WHERE amc.organization_id = ? AND amc.model_scope = ? AND amc.credits > 0
		UNION ALL
		SELECT cl.id::text, ?, cl.created_at, cl.amount, '', '', '', '', 0, 0
		FROM credit_lots AS cl WHERE cl.organization_id = ? AND cl.source = ?
		UNION ALL
		SELECT cm.id::text, ?, cm.created_at, cm.amount, '', '', '', '', 0, 0
		FROM credit_movements AS cm WHERE cm.organization_id = ? AND cm.source_type = ?
		UNION ALL
		SELECT cl.id::text, ?, cl.expires_at, -cl.remaining, '', '', '', '', 0, 0
		FROM credit_lots AS cl WHERE cl.organization_id = ? AND cl.remaining > 0 AND cl.expires_at <= ?`,
		domain.CreditEntryKindDailyGrant, organizationID, domain.CreditLotSourceDailyGrant,
		domain.CreditEntryKindAdjustment, organizationID,
		domain.CreditEntryKindModelCall, organizationID, domain.AIModelScopePlatform,
		domain.CreditEntryKindPurchase, organizationID, domain.CreditLotSourcePurchase,
		domain.CreditEntryKindRefund, organizationID, domain.CreditMovementSourceRefund,
		domain.CreditEntryKindExpiration, organizationID, now,
	)
	var total int
	if err := db.NewSelect().TableExpr("(?) AS entries", entries).ColumnExpr("count(*)").Scan(ctx, &total); err != nil {
		return EntryList{}, fmt.Errorf("count credit entries: %w", err)
	}
	list := EntryList{Entries: make([]Entry, 0), Page: common.PageInfo{Number: page, Size: pageSize, Total: total}}
	if err := db.NewSelect().TableExpr("(?) AS entries", entries).ColumnExpr("entries.*").
		// 同一事务内写入的记录时间相同，扣除排在入账上方。
		OrderExpr("entries.occurred_at DESC, entries.amount ASC, entries.id DESC").
		Limit(pageSize).
		Offset((page-1)*pageSize).
		Scan(ctx, &list.Entries); err != nil {
		return EntryList{}, fmt.Errorf("list credit entries: %w", err)
	}
	return list, nil
}

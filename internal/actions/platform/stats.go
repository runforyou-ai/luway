//go:build server

package platform

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/uptrace/bun"
)

const (
	// AggregateStatsActionName 是运营数据按日汇总任务的 Action 名称。
	AggregateStatsActionName = "platform.aggregate_stats"
	// StatsScheduleKey 是运营数据按日汇总的定时计划标识。
	StatsScheduleKey = "platform.stats"
	// statsQueue 是运营数据汇总任务使用的维护队列。
	statsQueue = "maintenance"
	// statsLockKey 是运营数据汇总使用的事务级咨询锁编号，汇总与全部重建串行执行。
	statsLockKey = 7_302_021_003
)

// AggregateStatsInput 是运营数据按日汇总任务的输入。
type AggregateStatsInput struct{}

// AggregateStatsAction 从业务记录汇总账号按日活跃明细和工作区按日指标。
type AggregateStatsAction struct {
	db *bun.DB
}

// NewAggregateStatsAction 创建运营数据按日汇总操作。
func NewAggregateStatsAction(db *bun.DB) *AggregateStatsAction {
	return &AggregateStatsAction{db: db}
}

// Execute 在运营数据汇总锁内读取平台时区后重建昨天和今天；平台带有重建标记时清空后从安装日起全部重建并清除标记。今天写入当前规模与截至此刻的活跃数据，其余日期只重算活跃数据；平台尚未完成首次安装时不执行。
func (a *AggregateStatsAction) Execute(ctx context.Context, _ AggregateStatsInput) error {
	return a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(?)", statsLockKey); err != nil {
			return fmt.Errorf("lock platform stats: %w", err)
		}
		platform, err := Load(ctx, tx)
		if errors.Is(err, ErrNotInstalled) {
			return nil
		}
		if err != nil {
			return err
		}
		location := statsLocation(platform.TimeZone)
		today := statsToday(time.Now(), location)
		from := today.AddDate(0, 0, -1)
		rebuildAll := platform.StatisticsRebuildPending
		if rebuildAll {
			for _, table := range []string{"account_daily_activities", "workspace_daily_stats"} {
				if _, err := tx.NewDelete().TableExpr(table).Where("TRUE").Exec(ctx); err != nil {
					return fmt.Errorf("clear %s: %w", table, err)
				}
			}
			from = statsToday(platform.CreatedAt, location)
		} else {
			for _, table := range []struct{ name, column string }{{"account_daily_activities", "activity_date"}, {"workspace_daily_stats", "stat_date"}} {
				if _, err := tx.NewDelete().TableExpr(table.name).Where("? > ?::date", bun.Ident(table.column), today.Format(time.DateOnly)).Exec(ctx); err != nil {
					return fmt.Errorf("delete future %s: %w", table.name, err)
				}
			}
		}
		for day := from; !day.After(today); day = day.AddDate(0, 0, 1) {
			if err := aggregateDay(ctx, tx, day, day.Equal(today)); err != nil {
				return fmt.Errorf("aggregate platform stats for %s: %w", day.Format(time.DateOnly), err)
			}
		}
		if rebuildAll {
			// 重建期间平台时区再次修改时保留标记，由随之投递的重建任务清除。
			if _, err := tx.NewUpdate().Model(platform).Set("statistics_rebuild_pending = FALSE").WherePK().
				Where("time_zone = ?", platform.TimeZone).Exec(ctx); err != nil {
				return fmt.Errorf("clear stats rebuild flag: %w", err)
			}
		}
		return nil
	})
}

// aggregateDay 按当前平台时区重建一天的账号活跃明细并写入工作区按日指标；day 是平台时区当天零点，current 为真时同时刷新规模字段。
func aggregateDay(ctx context.Context, tx bun.Tx, day time.Time, current bool) error {
	date := day.Format(time.DateOnly)
	lower, upper := serverstorage.MessageIDLowerBound(day), serverstorage.MessageIDLowerBound(day.AddDate(0, 0, 1))
	if _, err := tx.NewDelete().TableExpr("account_daily_activities").Where("activity_date = ?::date", date).Exec(ctx); err != nil {
		return fmt.Errorf("delete account daily activities: %w", err)
	}
	// 成员发送的消息按发送者计入，客服处理周期的系统事件按操作人计入。
	if _, err := tx.NewRaw(`
		INSERT INTO account_daily_activities (organization_id, activity_date, account_id)
		SELECT DISTINCT u.organization_id, ?::date, u.account_id
		FROM messages AS m
		LEFT JOIN conversation_participants AS cp ON cp.id = m.sender_participant_id
		LEFT JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.kind = ?
		JOIN users AS u ON u.organization_id = m.organization_id
			AND u.identity_id = CASE
				WHEN m.type = ? THEN NULLIF(m.system_event_payload->>'actorIdentityId', '')::uuid
				ELSE cs.source_id
			END
		WHERE m.id >= ? AND m.id < ?
		ON CONFLICT DO NOTHING
	`, date, domain.ChatSubjectKindOrganizationIdentity, domain.MessageTypeSystem, lower, upper).Exec(ctx); err != nil {
		return fmt.Errorf("insert account daily activities: %w", err)
	}
	rows := workspaceScaleColumns(tx.NewSelect().TableExpr("organizations AS o").
		ColumnExpr("o.id, ?::date", date)).
		ColumnExpr("(SELECT count(*) FROM account_daily_activities AS ada WHERE ada.organization_id = o.id AND ada.activity_date = ?::date)", date).
		ColumnExpr("(SELECT count(*) FROM messages AS m WHERE m.organization_id = o.id AND m.id >= ? AND m.id < ? AND m.type IN (?))",
			lower, upper, bun.In([]domain.MessageType{domain.MessageTypeText, domain.MessageTypeAttachment})).
		Where("o.created_at < ?", day.AddDate(0, 0, 1))
	if _, err := tx.NewRaw(`
		INSERT INTO workspace_daily_stats (
			organization_id, stat_date, member_count, ai_employee_count, channel_count, device_count, storage_bytes,
			active_account_count, message_count
		)
		?
		ON CONFLICT (stat_date, organization_id) DO UPDATE SET
			updated_at = now(),
			active_account_count = EXCLUDED.active_account_count,
			message_count = EXCLUDED.message_count,
			member_count = CASE WHEN ? THEN EXCLUDED.member_count ELSE workspace_daily_stats.member_count END,
			ai_employee_count = CASE WHEN ? THEN EXCLUDED.ai_employee_count ELSE workspace_daily_stats.ai_employee_count END,
			channel_count = CASE WHEN ? THEN EXCLUDED.channel_count ELSE workspace_daily_stats.channel_count END,
			device_count = CASE WHEN ? THEN EXCLUDED.device_count ELSE workspace_daily_stats.device_count END,
			storage_bytes = CASE WHEN ? THEN EXCLUDED.storage_bytes ELSE workspace_daily_stats.storage_bytes END
	`, rows, current, current, current, current, current).Exec(ctx); err != nil {
		return fmt.Errorf("upsert workspace daily stats: %w", err)
	}
	return nil
}

// statsLocation 返回平台时区，名称无法解析时使用 UTC。
func statsLocation(name string) *time.Location {
	location, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return location
}

// statsToday 返回时刻在平台时区所在日期的零点。
func statsToday(now time.Time, location *time.Location) time.Time {
	local := now.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
}

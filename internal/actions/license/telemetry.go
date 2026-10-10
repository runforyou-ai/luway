//go:build server

package license

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	"github.com/runforyou-ai/luway/internal/integration/control"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/uptrace/bun"
)

const (
	// ReportMetricsActionName 是向 control 上报一次运行指标的任务 Action 名称。
	ReportMetricsActionName = "platform.report_metrics"
	// ReportMetricsScheduleKey 是每分钟上报一次运行指标的定时计划标识。
	ReportMetricsScheduleKey = "platform-metrics-report"
)

// ReportMetricsAction 采集整个部署的运行指标并向 control 上报一次，由每分钟的定时任务在部署中的一台服务器上执行。
type ReportMetricsAction struct {
	db      *bun.DB
	control *control.Client
}

// NewReportMetricsAction 创建运行指标上报操作。
func NewReportMetricsAction(db *bun.DB, client *control.Client) *ReportMetricsAction {
	return &ReportMetricsAction{db: db, control: client}
}

// Execute 平台已安装且开启上报时按数据库当前时刻采集运行指标并上报；上报失败只记录警告，由下一分钟的任务重新上报。
func (a *ReportMetricsAction) Execute(ctx context.Context, _ struct{}) error {
	gauges, at, err := a.collect(ctx)
	if err != nil || gauges == nil {
		return err
	}
	if err := a.control.ExportMetrics(ctx, at, gauges); err != nil {
		slog.WarnContext(ctx, "上报运行指标失败", "error", err)
	}
	return nil
}

// collect 在只读事务中读取平台行与数据库当前时刻并统计各指标；平台未安装或关闭上报时返回 nil。
func (a *ReportMetricsAction) collect(ctx context.Context) ([]control.Gauge, time.Time, error) {
	var gauges []control.Gauge
	var now time.Time
	err := a.db.RunInTx(ctx, &sql.TxOptions{ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		platform, err := platformaction.Load(ctx, tx)
		if errors.Is(err, platformaction.ErrNotInstalled) {
			return nil
		}
		if err != nil {
			return err
		}
		if !platform.TelemetryEnabled {
			return nil
		}
		if now, err = serverstorage.Now(ctx, tx); err != nil {
			return err
		}
		var accounts, workspaces, members int64
		if err := platformaction.ScaleQuery(tx).Scan(ctx, &accounts, &workspaces, &members); err != nil {
			return fmt.Errorf("count platform scale: %w", err)
		}
		today := platformaction.LocalDay(now, platformaction.Location(platform.TimeZone))
		var activity platformaction.ActivityWindow
		if err := platformaction.ActivityQuery(tx, today.AddDate(0, 0, -6), today, platform.TimeZone).Scan(ctx, &activity); err != nil {
			return fmt.Errorf("read platform activity window: %w", err)
		}
		// 指标名称与 control 约定的一致。
		gauges = []control.Gauge{
			{Name: "platform.accounts", Description: "平台账号数", Value: accounts},
			{Name: "platform.workspaces", Description: "正常与暂停的工作区数", Value: workspaces},
			{Name: "platform.members", Description: "正常与暂停工作区中的有效成员数", Value: members},
			{Name: "platform.accounts.active_7d", Description: "近 7 天活跃账号数", Value: int64(activity.ActiveAccounts)},
			{Name: "platform.workspaces.active_7d", Description: "近 7 天活跃工作区数", Value: int64(activity.ActiveWorkspaces)},
		}
		return nil
	})
	return gauges, now, err
}

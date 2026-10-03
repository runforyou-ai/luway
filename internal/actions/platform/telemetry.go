//go:build server

package platform

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/integration/control"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// 向 control 上报的运行指标名称，与 control 约定的指标名一致。
const (
	metricAccounts         = "platform.accounts"
	metricWorkspaces       = "platform.workspaces"
	metricMembers          = "platform.members"
	metricActiveAccounts   = "platform.accounts.active_7d"
	metricActiveWorkspaces = "platform.workspaces.active_7d"
)

// TelemetryGauges 是向 control 上报的运行指标定义。
var TelemetryGauges = []control.Gauge{
	{Name: metricAccounts, Description: "平台账号数"},
	{Name: metricWorkspaces, Description: "正常与暂停的工作区数"},
	{Name: metricMembers, Description: "正常与暂停工作区中的有效成员数"},
	{Name: metricActiveAccounts, Description: "近 7 天活跃账号数"},
	{Name: metricActiveWorkspaces, Description: "近 7 天活跃工作区数"},
}

// TelemetryMetrics 返回运行指标的当前值；平台尚未完成首次安装或关闭了指标上报时返回 nil。
func TelemetryMetrics(ctx context.Context, db bun.IDB) (map[string]int64, error) {
	platform, err := Load(ctx, db)
	if errors.Is(err, ErrNotInstalled) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !platform.TelemetryEnabled {
		return nil, nil
	}
	var accounts, workspaces, members int64
	if err := scaleQuery(db).Scan(ctx, &accounts, &workspaces, &members); err != nil {
		return nil, fmt.Errorf("count platform scale: %w", err)
	}
	today := statsToday(time.Now(), statsLocation(platform.TimeZone))
	var activity ActivityWindow
	if err := activityQuery(db, today.AddDate(0, 0, -6), today, platform.TimeZone).Scan(ctx, &activity); err != nil {
		return nil, fmt.Errorf("read platform activity window: %w", err)
	}
	return map[string]int64{
		metricAccounts: accounts, metricWorkspaces: workspaces, metricMembers: members,
		metricActiveAccounts: int64(activity.ActiveAccounts), metricActiveWorkspaces: int64(activity.ActiveWorkspaces),
	}, nil
}

// UpdateTelemetryAction 开启或关闭运行指标上报。
type UpdateTelemetryAction struct {
	db *bun.DB
}

// NewUpdateTelemetryAction 创建运行指标上报开关修改操作。
func NewUpdateTelemetryAction(db *bun.DB) *UpdateTelemetryAction {
	return &UpdateTelemetryAction{db: db}
}

// Execute 由仍有效的平台管理员保存运行指标上报开关。
func (a *UpdateTelemetryAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, enabled bool) (Settings, error) {
	return updatePlatform(ctx, a.db, operator, func(ctx context.Context, tx bun.Tx, platform *servermodels.Platform) error {
		platform.TelemetryEnabled = enabled
		_, err := tx.NewUpdate().Model(platform).
			Column("telemetry_enabled").
			Set("updated_at = now()").
			WherePK().
			Exec(ctx)
		return err
	})
}

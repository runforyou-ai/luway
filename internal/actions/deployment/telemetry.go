//go:build server

package deployment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/integration/control"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// 向 control 上报的运行指标名称。
const (
	metricAccounts         = "deployment.accounts"
	metricWorkspaces       = "deployment.workspaces"
	metricMembers          = "deployment.members"
	metricActiveAccounts   = "deployment.accounts.active_7d"
	metricActiveWorkspaces = "deployment.workspaces.active_7d"
)

// TelemetryGauges 是向 control 上报的运行指标定义。
var TelemetryGauges = []control.Gauge{
	{Name: metricAccounts, Description: "部署账号数"},
	{Name: metricWorkspaces, Description: "正常与暂停的工作区数"},
	{Name: metricMembers, Description: "正常与暂停工作区中的有效成员数"},
	{Name: metricActiveAccounts, Description: "近 7 天活跃账号数"},
	{Name: metricActiveWorkspaces, Description: "近 7 天活跃工作区数"},
}

// TelemetryMetrics 返回运行指标的当前值；部署尚未完成首次安装或关闭了指标上报时返回 nil。
func TelemetryMetrics(ctx context.Context, db bun.IDB) (map[string]int64, error) {
	deployment, err := Load(ctx, db)
	if errors.Is(err, ErrNotInstalled) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !deployment.TelemetryEnabled {
		return nil, nil
	}
	var accounts, workspaces, members int64
	if err := scaleQuery(db).Scan(ctx, &accounts, &workspaces, &members); err != nil {
		return nil, fmt.Errorf("count deployment scale: %w", err)
	}
	today := statsToday(time.Now(), statsLocation(deployment.StatisticsTimeZone))
	var activity ActivityWindow
	if err := activityQuery(db, today.AddDate(0, 0, -6), today, deployment.StatisticsTimeZone).Scan(ctx, &activity); err != nil {
		return nil, fmt.Errorf("read deployment activity window: %w", err)
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

// Execute 由仍有效的部署管理员保存运行指标上报开关。
func (a *UpdateTelemetryAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, enabled bool) (Settings, error) {
	return updateDeployment(ctx, a.db, operator, func(ctx context.Context, tx bun.Tx, deployment *servermodels.Deployment) error {
		deployment.TelemetryEnabled = enabled
		_, err := tx.NewUpdate().Model(deployment).
			Column("telemetry_enabled").
			Set("updated_at = now()").
			WherePK().
			Exec(ctx)
		return err
	})
}

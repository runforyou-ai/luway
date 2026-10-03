//go:build server

package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
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

// telemetryRefreshInterval 是服务端实例重新读取上报开关的间隔。
const telemetryRefreshInterval = time.Minute

// Telemetry 缓存平台是否向 control 上报运行指标与错误，并采集运行指标；平台尚未完成首次安装时视为关闭。
// 修改开关的实例立即生效，其他实例在下次刷新时生效。
type Telemetry struct {
	db      bun.IDB
	enabled atomic.Bool
	// mu 串行化刷新的读写与本实例的修改，缓存始终是本实例最后一次读取或保存的开关。
	mu sync.Mutex
}

// NewTelemetry 创建上报开关缓存，初始为关闭。
func NewTelemetry(db bun.IDB) *Telemetry {
	return &Telemetry{db: db}
}

// Enabled 返回当前是否向 control 上报。
func (t *Telemetry) Enabled() bool {
	return t.enabled.Load()
}

// Refresh 从平台设置读取上报开关。
func (t *Telemetry) Refresh(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	platform, err := Load(ctx, t.db)
	if errors.Is(err, ErrNotInstalled) {
		t.enabled.Store(false)
		return nil
	}
	if err != nil {
		return err
	}
	t.enabled.Store(platform.TelemetryEnabled)
	return nil
}

// Run 按固定间隔刷新上报开关直到 ctx 结束，读取失败时保留上次的值。
func (t *Telemetry) Run(ctx context.Context) {
	ticker := time.NewTicker(telemetryRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := t.Refresh(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("读取上报开关失败", "error", err)
			}
		}
	}
}

// Metrics 返回运行指标的当前值；关闭上报时返回 nil。
func (t *Telemetry) Metrics(ctx context.Context) (map[string]int64, error) {
	if !t.Enabled() {
		return nil, nil
	}
	platform, err := Load(ctx, t.db)
	if err != nil {
		return nil, err
	}
	var accounts, workspaces, members int64
	if err := scaleQuery(t.db).Scan(ctx, &accounts, &workspaces, &members); err != nil {
		return nil, fmt.Errorf("count platform scale: %w", err)
	}
	today := statsToday(time.Now(), statsLocation(platform.StatisticsTimeZone))
	var activity ActivityWindow
	if err := activityQuery(t.db, today.AddDate(0, 0, -6), today, platform.StatisticsTimeZone).Scan(ctx, &activity); err != nil {
		return nil, fmt.Errorf("read platform activity window: %w", err)
	}
	return map[string]int64{
		metricAccounts: accounts, metricWorkspaces: workspaces, metricMembers: members,
		metricActiveAccounts: int64(activity.ActiveAccounts), metricActiveWorkspaces: int64(activity.ActiveWorkspaces),
	}, nil
}

// UpdateTelemetryAction 开启或关闭向 control 上报运行指标与错误。
type UpdateTelemetryAction struct {
	db        *bun.DB
	telemetry *Telemetry
}

// NewUpdateTelemetryAction 创建上报开关修改操作，保存后同步更新本实例的上报开关缓存。
func NewUpdateTelemetryAction(db *bun.DB, telemetry *Telemetry) *UpdateTelemetryAction {
	return &UpdateTelemetryAction{db: db, telemetry: telemetry}
}

// Execute 由仍有效的平台管理员保存上报开关；保存事务与缓存写入在同一把锁内完成。
func (a *UpdateTelemetryAction) Execute(ctx context.Context, operator *servermodels.AccountIdentity, enabled bool) (Settings, error) {
	a.telemetry.mu.Lock()
	defer a.telemetry.mu.Unlock()
	settings, err := updatePlatform(ctx, a.db, operator, func(ctx context.Context, tx bun.Tx, platform *servermodels.Platform) error {
		platform.TelemetryEnabled = enabled
		_, err := tx.NewUpdate().Model(platform).
			Column("telemetry_enabled").
			Set("updated_at = now()").
			WherePK().
			Exec(ctx)
		return err
	})
	if err != nil {
		return Settings{}, err
	}
	a.telemetry.enabled.Store(settings.TelemetryEnabled)
	return settings, nil
}

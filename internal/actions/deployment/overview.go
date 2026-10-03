//go:build server

package deployment

import (
	"context"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// overviewTrendDays 是部署概况趋势覆盖的天数，含今天。
const overviewTrendDays = 30

// Overview 定义部署实例的标识、安装时间、规模、活跃情况和当前能力；StatsRebuilding 表示正在按新统计时区重建运营数据。
type Overview struct {
	InstanceID         string
	InstalledAt        time.Time
	StatisticsTimeZone string
	StatsRebuilding    bool
	AccountCount       int
	WorkspaceCount     int
	MemberCount        int
	Last7Days          ActivityWindow
	Last30Days         ActivityWindow
	Trend              []DailyActivity
	Capabilities       domain.InstanceCapabilities
}

// ActivityWindow 定义截至今天的若干天内去重后的活跃账号数、活跃工作区数以及新增账号数和新增工作区数。
type ActivityWindow struct {
	ActiveAccounts   int `bun:"active_accounts"`
	ActiveWorkspaces int `bun:"active_workspaces"`
	NewAccounts      int `bun:"new_accounts"`
	NewWorkspaces    int `bun:"new_workspaces"`
}

// DailyActivity 定义统计时区一天内的活跃账号数、活跃工作区数、新增账号数和新增工作区数。
type DailyActivity struct {
	Date             time.Time `bun:"day"`
	ActiveAccounts   int       `bun:"active_accounts"`
	ActiveWorkspaces int       `bun:"active_workspaces"`
	NewAccounts      int       `bun:"new_accounts"`
	NewWorkspaces    int       `bun:"new_workspaces"`
}

// OverviewQuery 读取部署概况。
type OverviewQuery struct {
	db *bun.DB
}

// NewOverviewQuery 创建部署概况查询。
func NewOverviewQuery(db *bun.DB) *OverviewQuery {
	return &OverviewQuery{db: db}
}

// Execute 返回实例标识、安装时间、规模、近 7 天与近 30 天的活跃和新增情况、近 30 天逐日趋势和实例能力。
func (q *OverviewQuery) Execute(ctx context.Context) (Overview, error) {
	deployment, err := Load(ctx, q.db)
	if err != nil {
		return Overview{}, err
	}
	overview := Overview{
		InstanceID: deployment.InstanceID, InstalledAt: deployment.CreatedAt,
		StatisticsTimeZone: deployment.StatisticsTimeZone, StatsRebuilding: deployment.StatisticsRebuildPending,
	}
	if err := scaleQuery(q.db).Scan(ctx, &overview.AccountCount, &overview.WorkspaceCount, &overview.MemberCount); err != nil {
		return Overview{}, fmt.Errorf("count deployment scale: %w", err)
	}
	today := statsToday(time.Now(), statsLocation(deployment.StatisticsTimeZone))
	for _, window := range []struct {
		days   int
		target *ActivityWindow
	}{{7, &overview.Last7Days}, {30, &overview.Last30Days}} {
		if err := activityQuery(q.db, today.AddDate(0, 0, 1-window.days), today, deployment.StatisticsTimeZone).
			Scan(ctx, window.target); err != nil {
			return Overview{}, fmt.Errorf("read deployment activity window: %w", err)
		}
	}
	// 逐日趋势按日期生成序列，没有活跃或新增的日期取 0。
	overview.Trend = make([]DailyActivity, 0, overviewTrendDays)
	if err := q.db.NewRaw(`
		WITH days AS (SELECT generate_series(?::date, ?::date, interval '1 day')::date AS day)
		SELECT days.day,
			(SELECT count(DISTINCT ada.account_id) FROM account_daily_activities AS ada WHERE ada.activity_date = days.day) AS active_accounts,
			(SELECT count(*) FROM workspace_daily_stats AS wds WHERE wds.stat_date = days.day AND (wds.active_account_count > 0 OR wds.message_count > 0)) AS active_workspaces,
			(SELECT count(*) FROM accounts AS acc WHERE (acc.created_at AT TIME ZONE ?)::date = days.day) AS new_accounts,
			(SELECT count(*) FROM organizations AS o WHERE (o.created_at AT TIME ZONE ?)::date = days.day) AS new_workspaces
		FROM days
		ORDER BY days.day
	`, today.AddDate(0, 0, 1-overviewTrendDays).Format(time.DateOnly), today.Format(time.DateOnly),
		deployment.StatisticsTimeZone, deployment.StatisticsTimeZone).
		Scan(ctx, &overview.Trend); err != nil {
		return Overview{}, fmt.Errorf("read deployment activity trend: %w", err)
	}
	capabilities, err := Capabilities(ctx, q.db)
	if err != nil {
		return Overview{}, err
	}
	overview.Capabilities = capabilities
	return overview, nil
}

// listedLifecycleStatuses 是计入部署规模的工作区状态。
var listedLifecycleStatuses = []domain.OrganizationLifecycleStatus{domain.OrganizationLifecycleActive, domain.OrganizationLifecycleSuspended}

// scaleQuery 返回部署账号数、工作区数和有效成员数的查询。
func scaleQuery(db bun.IDB) *bun.RawQuery {
	return db.NewRaw(`
		SELECT
			(SELECT count(*) FROM accounts),
			(SELECT count(*) FROM organizations WHERE lifecycle_status IN (?)),
			(SELECT count(*) FROM users AS u JOIN organizations AS o ON o.id = u.organization_id WHERE u.status = ? AND o.lifecycle_status IN (?))
	`, bun.In(listedLifecycleStatuses), domain.IdentityStatusActive, bun.In(listedLifecycleStatuses))
}

// activityQuery 返回统计时区 from 至 to（含）之间去重活跃账号数、活跃工作区数、新增账号数和新增工作区数的查询。
func activityQuery(db bun.IDB, from, to time.Time, timeZone string) *bun.RawQuery {
	fromDate, toDate := from.Format(time.DateOnly), to.Format(time.DateOnly)
	return db.NewRaw(`
		SELECT
			(SELECT count(DISTINCT ada.account_id) FROM account_daily_activities AS ada WHERE ada.activity_date BETWEEN ?::date AND ?::date) AS active_accounts,
			(SELECT count(DISTINCT wds.organization_id) FROM workspace_daily_stats AS wds
				WHERE wds.stat_date BETWEEN ?::date AND ?::date AND (wds.active_account_count > 0 OR wds.message_count > 0)) AS active_workspaces,
			(SELECT count(*) FROM accounts AS acc WHERE (acc.created_at AT TIME ZONE ?)::date BETWEEN ?::date AND ?::date) AS new_accounts,
			(SELECT count(*) FROM organizations AS o WHERE (o.created_at AT TIME ZONE ?)::date BETWEEN ?::date AND ?::date) AS new_workspaces
	`, fromDate, toDate, fromDate, toDate, timeZone, fromDate, toDate, timeZone, fromDate, toDate)
}

//go:build server

package integrationtest

import (
	"context"
	"testing"
	"time"
	"uuid"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/stretchr/testify/require"
)

// TestPlatformOperationsData 验证运营数据从消息推导活跃账号与活跃工作区，汇总可重复执行，平台概览与工作区列表按平台时区读取汇总结果。
func TestPlatformOperationsData(t *testing.T) {
	t.Parallel()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	tasks := servertest.NewTasks()
	aggregate := platformaction.NewAggregateStatsAction(db)
	backend := direct.New(db, direct.DeploymentConfig{Deployment: servertest.NewDeployment(t, db)}, nil, nil, nil, tasks, nil, nil, nil)
	service := backend
	ctx := context.Background()
	meta := appservice.RequestMeta{Locale: domain.LocaleChineseSimplified}

	installed, err := service.InstallWorkspace(ctx, meta, appservice.InstallWorkspaceInput{
		PublicURL: servertest.PublicURL, WorkspaceName: "运营数据", DisplayName: "管理员", Email: "admin@example.test",
		Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	admin := installed.Auth
	require.NoError(t, err)
	adminMeta := appservice.RequestMeta{Token: admin.Token, Locale: domain.LocaleChineseSimplified}
	settings, err := backend.GetPlatformSettings(ctx, adminMeta)
	require.NoError(t, err)
	deployment, err := backend.GetPlatformDeployment(ctx, adminMeta)
	require.NoError(t, err)
	require.Equal(t, "Asia/Shanghai", deployment.TimeZone)
	_, err = backend.UpdatePlatformDeploymentBasics(ctx, adminMeta, appservice.PlatformDeploymentBasicsInput{
		Name: deployment.Name, TimeZone: "Mars/Olympus", TelemetryEnabled: deployment.TelemetryEnabled,
	})
	servertest.RequireFieldError(t, err, "timeZone", i18n.FieldTimeZoneInvalid)

	workspaces, err := backend.ListWorkspaces(ctx, adminMeta)
	require.NoError(t, err)
	require.Len(t, workspaces.Items, 1)
	require.Equal(t, appservice.WorkspaceStatusActive, workspaces.Items[0].Status)
	workspaceID := workspaces.Items[0].ID
	owner := servertest.ResolveMemberSession(t, db, workspaceID, admin.Token).Identity
	memberEmail := servertest.UniqueEmail("member")
	_, err = newTestMemberCreator(db, testEnqueuer).Execute(ctx, owner, memberSpec{DisplayName: "成员", Email: memberEmail, Password: "password123", RoleID: owner.User.RoleID})
	require.NoError(t, err)
	member := servertest.LoginMember(t, db, workspaceID, memberEmail, "password123")
	idle := servertest.AddAccountWorkspace(t, db, admin.Token, "空闲工作区")

	// 平台管理员在运营数据工作区发送单聊消息，成员只收到消息。
	_, err = directchataction.NewSendFirstDirectTextMessageAction(db, testEnqueuer).Execute(ctx, owner, directchataction.FirstDirectTextMessageInput{
		TargetIdentityID: member.Identity.WorkspaceIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "今天的安排",
	})
	require.NoError(t, err)
	for range 2 {
		require.NoError(t, aggregate.Execute(ctx, platformaction.AggregateStatsInput{}))
	}

	overview, err := backend.GetPlatformOverview(ctx, adminMeta)
	require.NoError(t, err)
	shanghai, _ := time.LoadLocation("Asia/Shanghai")
	today := time.Now().In(shanghai).Format(time.DateOnly)
	type overviewSummary struct {
		TimeZone                                                               string
		AccountCount, WorkspaceCount, MemberCount                              int
		ActiveAccounts7d, ActiveWorkspaces7d, NewAccounts30d, NewWorkspaces30d int
	}
	require.Equal(t, overviewSummary{"Asia/Shanghai", 2, 2, 3, 1, 1, 2, 2}, overviewSummary{
		overview.TimeZone, overview.AccountCount, overview.WorkspaceCount, overview.MemberCount,
		overview.Last7Days.ActiveAccounts, overview.Last7Days.ActiveWorkspaces, overview.Last30Days.NewAccounts, overview.Last30Days.NewWorkspaces,
	})
	require.Len(t, overview.Trend, 30)
	last := overview.Trend[29]
	type trendSummary struct {
		Date                                            string
		ActiveAccounts, ActiveWorkspaces, NewWorkspaces int
	}
	require.Equal(t, trendSummary{today, 1, 1, 2}, trendSummary{last.Date, last.ActiveAccounts, last.ActiveWorkspaces, last.NewWorkspaces})

	list, err := backend.ListPlatformWorkspaces(ctx, adminMeta, appservice.PlatformWorkspaceListInput{Sort: appservice.PlatformWorkspaceSortLastActive})
	require.NoError(t, err)
	require.Len(t, list.Workspaces, 2)
	active, quiet := list.Workspaces[0], list.Workspaces[1]
	require.Equal(t, workspaceID, active.ID)
	require.Equal(t, 2, active.MemberCount)
	require.NotNil(t, active.LastActiveDays)
	require.Equal(t, 0, *active.LastActiveDays)
	require.Equal(t, idle.Workspace.ID, quiet.ID)
	require.Equal(t, 1, quiet.MemberCount)
	require.Nil(t, quiet.LastActiveDays)
	// 按旧时区统计的日期晚于平台时区今天时，距今天数取 0。
	_, err = db.NewRaw(`INSERT INTO workspace_daily_stats (workspace_id, stat_date, member_count, ai_employee_count, channel_count,
		computer_count, storage_bytes, active_account_count, message_count) VALUES (?, current_date + 3, 1, 0, 0, 0, 0, 1, 0)`, idle.Workspace.ID).Exec(ctx)
	require.NoError(t, err)
	ahead, err := backend.ListPlatformWorkspaces(ctx, adminMeta, appservice.PlatformWorkspaceListInput{Sort: appservice.PlatformWorkspaceSortLastActive})
	require.NoError(t, err)
	require.Equal(t, idle.Workspace.ID, ahead.Workspaces[0].ID)
	require.NotNil(t, ahead.Workspaces[0].LastActiveDays)
	require.Equal(t, 0, *ahead.Workspaces[0].LastActiveDays)
	_, err = db.NewRaw("DELETE FROM workspace_daily_stats WHERE workspace_id = ? AND stat_date > current_date", idle.Workspace.ID).Exec(ctx)
	require.NoError(t, err)
	_, err = backend.ListPlatformWorkspaces(ctx, adminMeta, appservice.PlatformWorkspaceListInput{Sort: "name"})
	servertest.RequireFieldError(t, err, "sort", i18n.FieldPlatformQueryInvalid)

	// 修改平台时区后从安装日起按新时区重建：同一条消息只计入新时区的一天，没有消息支撑的历史活跃被清除。
	_, err = db.NewRaw("INSERT INTO account_daily_activities (workspace_id, activity_date, account_id) VALUES (?, current_date - 10, ?)",
		workspaceID, admin.Account.ID).Exec(ctx)
	require.NoError(t, err)
	_, err = db.NewRaw("UPDATE platforms SET created_at = now() - interval '20 days'").Exec(ctx)
	require.NoError(t, err)
	// 修改平台时区只更新部署基本配置，注册策略保持不变。
	_, err = backend.UpdatePlatformSettings(ctx, adminMeta, appservice.PlatformPoliciesInput{
		RegistrationPolicy: domain.RegistrationPolicyOpen, WorkspaceCreationPolicy: settings.WorkspaceCreationPolicy,
	})
	require.NoError(t, err)
	updated, err := backend.UpdatePlatformDeploymentBasics(ctx, adminMeta, appservice.PlatformDeploymentBasicsInput{
		Name: deployment.Name, TimeZone: "Pacific/Pago_Pago", TelemetryEnabled: deployment.TelemetryEnabled,
	})
	require.NoError(t, err)
	require.Equal(t, "Pacific/Pago_Pago", updated.TimeZone)
	policies, err := backend.GetPlatformSettings(ctx, adminMeta)
	require.NoError(t, err)
	require.Equal(t, domain.RegistrationPolicyOpen, policies.RegistrationPolicy)
	require.Len(t, tasks.Queued(platformaction.AggregateStatsActionName, ""), 1, "stats rebuild tasks")
	rebuilding, err := backend.GetPlatformOverview(ctx, adminMeta)
	require.NoError(t, err)
	require.True(t, rebuilding.StatsRebuilding)
	require.NoError(t, aggregate.Execute(ctx, platformaction.AggregateStatsInput{}))
	rebuilt, err := backend.GetPlatformOverview(ctx, adminMeta)
	require.NoError(t, err)
	require.False(t, rebuilt.StatsRebuilding)
	require.Equal(t, "Pacific/Pago_Pago", rebuilt.TimeZone)
	pagoPago, _ := time.LoadLocation("Pacific/Pago_Pago")
	var dates []time.Time
	require.NoError(t, db.NewSelect().TableExpr("account_daily_activities").Column("activity_date").
		Where("account_id = ?", admin.Account.ID).Scan(ctx, &dates))
	require.Len(t, dates, 1)
	require.Equal(t, time.Now().In(pagoPago).Format(time.DateOnly), dates[0].Format(time.DateOnly))
}

// TestPlatformWorkspaceSuspension 验证平台管理员暂停工作区后成员无法进入、网站渠道停止接待客户，恢复后一切照常；有平台管理员成员的工作区不能暂停。
func TestPlatformWorkspaceSuspension(t *testing.T) {
	t.Parallel()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	backend := newAccountTestBackend(t, db)
	service := backend
	ctx := context.Background()
	meta := appservice.RequestMeta{Locale: domain.LocaleChineseSimplified}

	installed, err := service.InstallWorkspace(ctx, meta, appservice.InstallWorkspaceInput{
		PublicURL: servertest.PublicURL, WorkspaceName: "暂停验证", DisplayName: "管理员", Email: "admin@example.test",
		Password: "password123", Locale: domain.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	admin := installed.Auth
	require.NoError(t, err)
	adminMeta := appservice.RequestMeta{Token: admin.Token, Locale: domain.LocaleChineseSimplified}
	workspaces, err := backend.ListWorkspaces(ctx, adminMeta)
	require.NoError(t, err)
	require.Len(t, workspaces.Items, 1)
	adminWorkspaceID := workspaces.Items[0].ID
	owner := servertest.ResolveMemberSession(t, db, adminWorkspaceID, admin.Token).Identity
	memberEmail := servertest.UniqueEmail("member")
	_, err = newTestMemberCreator(db, testEnqueuer).Execute(ctx, owner, memberSpec{DisplayName: "成员", Email: memberEmail, Password: "password123", RoleID: owner.User.RoleID})
	require.NoError(t, err)
	member := servertest.LoginMember(t, db, adminWorkspaceID, memberEmail, "password123")
	// 成员自己的工作区没有平台管理员成员，可以暂停。
	branch := servertest.AddAccountWorkspace(t, db, member.Token, "分部")
	workspaceID := branch.Workspace.ID
	memberMeta := appservice.RequestMeta{Token: member.Token, WorkspaceID: workspaceID, Locale: domain.LocaleChineseSimplified}
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, branch, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "官网", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	require.NoError(t, err)
	publicChannel := channelaction.NewGetPublicWebsiteChannelQuery(db)

	_, err = backend.SuspendPlatformWorkspace(ctx, memberMeta, workspaceID)
	servertest.RequireErrorKind(t, err, appservice.ErrorKindForbidden)
	_, err = backend.SuspendPlatformWorkspace(ctx, adminMeta, uuid.NewV7().String())
	servertest.RequireErrorKind(t, err, appservice.ErrorKindNotFound)
	_, err = backend.SuspendPlatformWorkspace(ctx, adminMeta, adminWorkspaceID)
	servertest.RequireErrorKind(t, err, appservice.ErrorKindInvalid)
	suspended, err := backend.SuspendPlatformWorkspace(ctx, adminMeta, workspaceID)
	require.NoError(t, err)
	require.Equal(t, appservice.WorkspaceStatusSuspended, suspended.Status)
	require.False(t, suspended.HasPlatformAdmin)
	require.Equal(t, 1, suspended.MemberCount)
	require.Equal(t, 1, suspended.ChannelCount)
	_, err = backend.LoadIdentity(ctx, memberMeta)
	requireSessionState(t, err, appservice.SessionStateWorkspace)
	joined, err := backend.ListWorkspaces(ctx, memberMeta)
	require.NoError(t, err)
	require.Len(t, joined.Items, 2)
	for _, item := range joined.Items {
		want := map[bool]appservice.WorkspaceStatus{true: appservice.WorkspaceStatusSuspended, false: appservice.WorkspaceStatusActive}[item.ID == workspaceID]
		require.Equal(t, want, item.Status, "member workspace %s", item.Name)
	}
	_, err = publicChannel.Execute(ctx, channel.ID)
	require.ErrorIs(t, err, channelaction.ErrNotFound)
	filtered, err := backend.ListPlatformWorkspaces(ctx, adminMeta, appservice.PlatformWorkspaceListInput{Status: appservice.WorkspaceStatusSuspended})
	require.NoError(t, err)
	require.EqualValues(t, 1, filtered.Page.Total)
	require.Equal(t, workspaceID, filtered.Workspaces[0].ID)

	resumed, err := backend.ResumePlatformWorkspace(ctx, adminMeta, workspaceID)
	require.NoError(t, err)
	require.Equal(t, appservice.WorkspaceStatusActive, resumed.Status)
	_, err = backend.LoadIdentity(ctx, memberMeta)
	require.NoError(t, err, "member identity after resume")
	_, err = publicChannel.Execute(ctx, channel.ID)
	require.NoError(t, err, "public channel after resume")
}

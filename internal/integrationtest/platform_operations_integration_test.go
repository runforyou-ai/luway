//go:build server

package integrationtest

import (
	"context"
	"errors"
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
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
)

// TestPlatformOperationsData 验证运营数据从消息推导活跃账号与活跃工作区，汇总可重复执行，平台概览与工作区列表按统计时区读取汇总结果。
func TestPlatformOperationsData(t *testing.T) {
	t.Parallel()
	db := openEmptyDatabase(t)
	tasks := newTestTasks(db)
	aggregate := platformaction.NewAggregateStatsAction(db)
	if err := tasks.Registry().RegisterJSON(platformaction.AggregateStatsActionName, aggregate.Execute); err != nil {
		t.Fatal(err)
	}
	backend := direct.New(db, direct.DeploymentConfig{PublicURL: testPublicURL}, nil, serverfilecontent.S3Config{}, nil, nil, tasks, nil, nil, nil)
	service := appservice.New(backend)
	ctx := context.Background()
	meta := appservice.RequestMeta{Locale: appservice.LocaleChineseSimplified}

	admin, err := service.InstallWorkspace(ctx, meta, appservice.InstallWorkspaceInput{
		WorkspaceName: "运营数据", WorkspaceSlug: "operations", DisplayName: "管理员", Email: "admin@example.test",
		Password: "password123", Locale: appservice.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	if err != nil {
		t.Fatal(err)
	}
	adminMeta := appservice.RequestMeta{Token: admin.Token, Locale: appservice.LocaleChineseSimplified}
	settings, err := backend.GetPlatformSettings(ctx, adminMeta)
	if err != nil || settings.StatisticsTimeZone != "Asia/Shanghai" {
		t.Fatalf("settings = %#v, err = %v", settings, err)
	}
	_, err = backend.UpdatePlatformStatisticsTimeZone(ctx, adminMeta, appservice.PlatformStatisticsTimeZoneInput{StatisticsTimeZone: "Mars/Olympus"})
	requireFieldError(t, err, "statisticsTimeZone", i18n.FieldTimeZoneInvalid)

	workspaces, err := backend.ListWorkspaces(ctx, adminMeta)
	if err != nil || len(workspaces.Items) != 1 || workspaces.Items[0].Status != appservice.WorkspaceStatusActive {
		t.Fatalf("workspaces = %#v, err = %v", workspaces, err)
	}
	workspaceID := workspaces.Items[0].ID
	owner := resolveMemberSession(t, db, workspaceID, admin.Token).Identity
	memberEmail := uniqueEmail("member")
	if _, err := newTestMemberCreator(db, newTestTasks(db)).Execute(ctx, owner, memberSpec{DisplayName: "成员", Email: memberEmail, Password: "password123", RoleID: owner.User.RoleID}); err != nil {
		t.Fatal(err)
	}
	member := loginMember(t, db, workspaceID, memberEmail, "password123")
	idle := addAccountWorkspace(t, db, admin.Token, "空闲工作区")

	// 平台管理员在运营数据工作区发送单聊消息，成员只收到消息。
	if _, err := directchataction.NewSendFirstDirectTextMessageAction(db).Execute(ctx, owner, directchataction.FirstDirectTextMessageInput{
		TargetIdentityID: member.Identity.OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "今天的安排",
	}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := aggregate.Execute(ctx, platformaction.AggregateStatsInput{}); err != nil {
			t.Fatal(err)
		}
	}

	overview, err := backend.GetPlatformOverview(ctx, adminMeta)
	if err != nil {
		t.Fatal(err)
	}
	shanghai, _ := time.LoadLocation("Asia/Shanghai")
	today := time.Now().In(shanghai).Format(time.DateOnly)
	if overview.StatisticsTimeZone != "Asia/Shanghai" || overview.AccountCount != 2 || overview.WorkspaceCount != 2 || overview.MemberCount != 3 ||
		overview.Last7Days.ActiveAccounts != 1 || overview.Last7Days.ActiveWorkspaces != 1 ||
		overview.Last30Days.NewAccounts != 2 || overview.Last30Days.NewWorkspaces != 2 || len(overview.Trend) != 30 {
		t.Fatalf("overview = %#v", overview)
	}
	if last := overview.Trend[29]; last.Date != today || last.ActiveAccounts != 1 || last.ActiveWorkspaces != 1 || last.NewWorkspaces != 2 {
		t.Fatalf("today trend = %#v, want date %s", last, today)
	}

	list, err := backend.ListPlatformWorkspaces(ctx, adminMeta, appservice.PlatformWorkspaceListInput{Sort: appservice.PlatformWorkspaceSortLastActive})
	if err != nil || len(list.Workspaces) != 2 {
		t.Fatalf("workspace list = %#v, err = %v", list, err)
	}
	active, quiet := list.Workspaces[0], list.Workspaces[1]
	if active.ID != workspaceID || active.MemberCount != 2 || active.LastActiveOn == nil || *active.LastActiveOn != today ||
		quiet.ID != idle.Organization.ID || quiet.MemberCount != 1 || quiet.LastActiveOn != nil {
		t.Fatalf("workspaces sorted by activity = %#v", list.Workspaces)
	}
	_, err = backend.ListPlatformWorkspaces(ctx, adminMeta, appservice.PlatformWorkspaceListInput{Sort: "name"})
	requireFieldError(t, err, "sort", i18n.FieldPlatformQueryInvalid)

	// 修改统计时区后从安装日起按新时区重建：同一条消息只计入新时区的一天，没有消息支撑的历史活跃被清除。
	if _, err := db.NewRaw("INSERT INTO account_daily_activities (organization_id, activity_date, account_id) VALUES (?, current_date - 10, ?)",
		workspaceID, admin.Account.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw("UPDATE platforms SET created_at = now() - interval '20 days'").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	// 修改统计时区只更新时区，注册策略保持不变。
	if _, err := backend.UpdatePlatformSettings(ctx, adminMeta, appservice.PlatformPoliciesInput{
		RegistrationPolicy: appservice.RegistrationPolicyOpen, WorkspaceCreationPolicy: settings.WorkspaceCreationPolicy,
	}); err != nil {
		t.Fatal(err)
	}
	if updated, err := backend.UpdatePlatformStatisticsTimeZone(ctx, adminMeta, appservice.PlatformStatisticsTimeZoneInput{StatisticsTimeZone: "Pacific/Pago_Pago"}); err != nil ||
		updated.StatisticsTimeZone != "Pacific/Pago_Pago" || updated.RegistrationPolicy != appservice.RegistrationPolicyOpen {
		t.Fatalf("updated settings = %#v, err = %v", updated, err)
	}
	rebuilds, err := db.NewSelect().TableExpr("task_runs").
		Where("action_name = ? AND schedule_key IS NULL", platformaction.AggregateStatsActionName).Count(ctx)
	if err != nil || rebuilds != 1 {
		t.Fatalf("rebuild tasks = %d, err = %v", rebuilds, err)
	}
	if rebuilding, err := backend.GetPlatformOverview(ctx, adminMeta); err != nil || !rebuilding.StatsRebuilding {
		t.Fatalf("overview before rebuild = %#v, err = %v", rebuilding, err)
	}
	if err := aggregate.Execute(ctx, platformaction.AggregateStatsInput{}); err != nil {
		t.Fatal(err)
	}
	if rebuilt, err := backend.GetPlatformOverview(ctx, adminMeta); err != nil || rebuilt.StatsRebuilding || rebuilt.StatisticsTimeZone != "Pacific/Pago_Pago" {
		t.Fatalf("overview after rebuild = %#v, err = %v", rebuilt, err)
	}
	pagoPago, _ := time.LoadLocation("Pacific/Pago_Pago")
	var dates []time.Time
	if err := db.NewSelect().TableExpr("account_daily_activities").Column("activity_date").
		Where("account_id = ?", admin.Account.ID).Scan(ctx, &dates); err != nil {
		t.Fatal(err)
	}
	if want := time.Now().In(pagoPago).Format(time.DateOnly); len(dates) != 1 || dates[0].Format(time.DateOnly) != want {
		t.Fatalf("activity dates after time zone change = %v, want [%s]", dates, want)
	}
}

// TestPlatformWorkspaceSuspension 验证平台管理员暂停工作区后成员无法进入、网站渠道停止接待客户，恢复后一切照常；有平台管理员成员的工作区不能暂停。
func TestPlatformWorkspaceSuspension(t *testing.T) {
	t.Parallel()
	db := openEmptyDatabase(t)
	backend := newAccountTestBackend(db)
	service := appservice.New(backend)
	ctx := context.Background()
	meta := appservice.RequestMeta{Locale: appservice.LocaleChineseSimplified}

	admin, err := service.InstallWorkspace(ctx, meta, appservice.InstallWorkspaceInput{
		WorkspaceName: "暂停验证", WorkspaceSlug: "suspension", DisplayName: "管理员", Email: "admin@example.test",
		Password: "password123", Locale: appservice.LocaleChineseSimplified, TimeZone: "Asia/Shanghai",
	})
	if err != nil {
		t.Fatal(err)
	}
	adminMeta := appservice.RequestMeta{Token: admin.Token, Locale: appservice.LocaleChineseSimplified}
	workspaces, err := backend.ListWorkspaces(ctx, adminMeta)
	if err != nil || len(workspaces.Items) != 1 {
		t.Fatalf("workspaces = %#v, err = %v", workspaces, err)
	}
	adminWorkspaceID := workspaces.Items[0].ID
	owner := resolveMemberSession(t, db, adminWorkspaceID, admin.Token).Identity
	memberEmail := uniqueEmail("member")
	if _, err := newTestMemberCreator(db, newTestTasks(db)).Execute(ctx, owner, memberSpec{DisplayName: "成员", Email: memberEmail, Password: "password123", RoleID: owner.User.RoleID}); err != nil {
		t.Fatal(err)
	}
	member := loginMember(t, db, adminWorkspaceID, memberEmail, "password123")
	// 成员自己的工作区没有平台管理员成员，可以暂停。
	branch := addAccountWorkspace(t, db, member.Token, "分部")
	workspaceID := branch.Organization.ID
	memberMeta := appservice.RequestMeta{Token: member.Token, WorkspaceID: workspaceID, Locale: appservice.LocaleChineseSimplified}
	channel, err := channelaction.NewCreateMessageChannelAction(db).Execute(ctx, branch, channelaction.CreateMessageChannelInput{
		Type: domain.ChannelTypeWebsite, Name: "官网", DefaultLocale: domain.CustomerLocaleChineseSimplified,
		NewConversationTarget: channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
	})
	if err != nil {
		t.Fatal(err)
	}
	publicChannel := channelaction.NewGetPublicWebsiteChannelQuery(db)

	_, err = backend.SuspendPlatformWorkspace(ctx, memberMeta, workspaceID)
	requireErrorKind(t, err, appservice.ErrorKindForbidden)
	_, err = backend.SuspendPlatformWorkspace(ctx, adminMeta, uuid.NewV7().String())
	requireErrorKind(t, err, appservice.ErrorKindNotFound)
	_, err = backend.SuspendPlatformWorkspace(ctx, adminMeta, adminWorkspaceID)
	requireErrorKind(t, err, appservice.ErrorKindInvalid)
	suspended, err := backend.SuspendPlatformWorkspace(ctx, adminMeta, workspaceID)
	if err != nil || suspended.Status != appservice.WorkspaceStatusSuspended || suspended.HasPlatformAdmin || suspended.MemberCount != 1 || suspended.ChannelCount != 1 {
		t.Fatalf("suspended = %#v, err = %v", suspended, err)
	}
	_, err = backend.LoadIdentity(ctx, memberMeta)
	requireSessionState(t, err, appservice.SessionStateWorkspace)
	joined, err := backend.ListWorkspaces(ctx, memberMeta)
	if err != nil || len(joined.Items) != 2 {
		t.Fatalf("member workspaces = %#v, err = %v", joined, err)
	}
	for _, item := range joined.Items {
		if want := map[bool]appservice.WorkspaceStatus{true: appservice.WorkspaceStatusSuspended, false: appservice.WorkspaceStatusActive}[item.ID == workspaceID]; item.Status != want {
			t.Fatalf("member workspace %s status = %q, want %q", item.Name, item.Status, want)
		}
	}
	if _, err := publicChannel.Execute(ctx, channel.ID); !errors.Is(err, channelaction.ErrNotFound) {
		t.Fatalf("suspended public channel err = %v", err)
	}
	filtered, err := backend.ListPlatformWorkspaces(ctx, adminMeta, appservice.PlatformWorkspaceListInput{Status: appservice.WorkspaceStatusSuspended})
	if err != nil || filtered.Page.Total != 1 || filtered.Workspaces[0].ID != workspaceID {
		t.Fatalf("suspended workspaces = %#v, err = %v", filtered, err)
	}

	resumed, err := backend.ResumePlatformWorkspace(ctx, adminMeta, workspaceID)
	if err != nil || resumed.Status != appservice.WorkspaceStatusActive {
		t.Fatalf("resumed = %#v, err = %v", resumed, err)
	}
	if _, err := backend.LoadIdentity(ctx, memberMeta); err != nil {
		t.Fatalf("member identity after resume: %v", err)
	}
	if _, err := publicChannel.Execute(ctx, channel.ID); err != nil {
		t.Fatalf("public channel after resume: %v", err)
	}
}

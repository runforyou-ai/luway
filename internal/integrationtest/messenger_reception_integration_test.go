//go:build server

package integrationtest

import (
	"context"
	"testing"
	"time"

	"uuid"

	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	"github.com/runforyou-ai/luway/internal/actions/serviceroute"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// messengerReceptionVisitor 是接待状态测试使用的访客渠道身份。
const messengerReceptionVisitor = "web-session:0123456789abcdef0123456789abcdef"

// directory 读取访客目录，返回新会话接待状态与测试会话的接待状态。
func (f executionScopeFixture) directory(t *testing.T, ctx context.Context) (serviceroute.Reception, serviceroute.Reception) {
	t.Helper()
	directory, err := customerchataction.NewListWebsiteConversationsQuery(f.db).Execute(ctx, f.channelID, messengerReceptionVisitor)
	require.NoError(t, err)
	for _, conversation := range directory.Conversations {
		if conversation.ID == f.conversationID {
			return directory.NewSessionReception, conversation.Reception
		}
	}
	require.FailNowf(t, "visitor conversation missing", "%+v", directory.Conversations)
	return serviceroute.Reception{}, serviceroute.Reception{}
}

// setWorkStatus 设置企业内全部真人成员或指定身份的工作状态。
func (f executionScopeFixture) setWorkStatus(t *testing.T, ctx context.Context, status domain.WorkStatus, identityIDs ...string) {
	t.Helper()
	query := f.db.NewUpdate().Model((*servermodels.WorkspaceIdentity)(nil)).
		Set("work_status = ?", status).
		Where("workspace_id = ? AND type = ?", f.owner.Workspace.ID, domain.WorkspaceIdentityTypeUser)
	if len(identityIDs) > 0 {
		query = query.Where("id IN (?)", bun.List(identityIDs))
	}
	_, err := query.Exec(ctx)
	require.NoError(t, err)
}

// setInitialRoute 修改测试渠道的新会话路由，失败目标保持公共队列。
func (f executionScopeFixture) setInitialRoute(t *testing.T, ctx context.Context, targetType domain.ChannelRoutingTargetType, targetID *string) {
	t.Helper()
	_, err := f.db.NewUpdate().Model((*servermodels.Channel)(nil)).
		Set("initial_routing_target_type = ?", targetType).
		Set("initial_routing_target_id = ?", targetID).
		Where("workspace_id = ? AND id = ?", f.owner.Workspace.ID, f.channelID).
		Exec(ctx)
	require.NoError(t, err)
}

// TestMessengerNewSessionReception 验证网站新会话按当前路由、工作状态与工作时间推导接待状态。
func TestMessengerNewSessionReception(t *testing.T) {
	t.Parallel()
	f := newExecutionScopeFixture(t)
	ctx := context.Background()

	// 公共队列有工作中的接待成员时在线并尽快回复，全员下班时离线。
	f.setWorkStatus(t, ctx, domain.WorkStatusOffDuty)
	f.setWorkStatus(t, ctx, domain.WorkStatusWorking, f.member.WorkspaceIdentity.ID)
	reception, _ := f.directory(t, ctx)
	require.Nil(t, reception.HandlerType, "queue reception")
	require.True(t, reception.Online, "queue reception")
	require.Equal(t, domain.CustomerReceptionReplySoon, reception.Reply, "queue reception")
	f.setWorkStatus(t, ctx, domain.WorkStatusOffDuty)
	reception, _ = f.directory(t, ctx)
	require.False(t, reception.Online, "offline queue reception")
	require.Equal(t, domain.CustomerReceptionReplySoon, reception.Reply, "offline queue reception")

	// 工作时间外按下个工作时段回复。
	now := time.Now().UTC()
	hours := domain.BusinessHours{Enabled: true, TimeZone: "UTC", Overrides: []domain.BusinessHoursOverride{{Date: now.Format(domain.BusinessHoursDateLayout)}}}
	for day := range hours.Weekly {
		hours.Weekly[day] = []domain.BusinessHoursPeriod{{Start: "00:00", End: "24:00"}}
	}
	_, err := customerserviceaction.NewUpdateBusinessHoursAction(f.db).Execute(ctx, f.owner, hours)
	require.NoError(t, err)
	f.setWorkStatus(t, ctx, domain.WorkStatusWorking)
	tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	reception, _ = f.directory(t, ctx)
	require.False(t, reception.Online, "after hours reception")
	require.Equal(t, domain.CustomerReceptionReplyScheduled, reception.Reply, "after hours reception")
	require.NotNil(t, reception.NextOpeningAt, "after hours reception")
	require.True(t, reception.NextOpeningAt.Equal(tomorrow), "next opening at = %v, want %v", reception.NextOpeningAt, tomorrow)
	// 目录同时给出工作时间开关的下一时刻，访客端到时重新读取。
	directory, err := customerchataction.NewListWebsiteConversationsQuery(f.db).Execute(ctx, f.channelID, messengerReceptionVisitor)
	require.NoError(t, err)
	require.NotNil(t, directory.ReceptionRefreshAt)
	require.True(t, directory.ReceptionRefreshAt.Equal(tomorrow), "reception refresh at = %v, want %v", directory.ReceptionRefreshAt, tomorrow)
	hours.Enabled = false
	_, err = customerserviceaction.NewUpdateBusinessHoursAction(f.db).Execute(ctx, f.owner, hours)
	require.NoError(t, err)

	// AI 员工首接待时始终在线并立即回复。
	f.setInitialRoute(t, ctx, domain.ChannelRoutingTargetTypeMember, &f.agentIdentityID)
	reception, _ = f.directory(t, ctx)
	require.Equal(t, new(domain.WorkspaceIdentityTypeAgent), reception.HandlerType, "agent reception")
	require.Equal(t, "执行范围助手", reception.HandlerName, "agent reception")
	require.True(t, reception.Online, "agent reception")
	require.Equal(t, domain.CustomerReceptionReplyImmediate, reception.Reply, "agent reception")

	// 真人首接待工作中时展示该成员，不在工作中时顺延到公共队列。
	f.setInitialRoute(t, ctx, domain.ChannelRoutingTargetTypeMember, &f.member.WorkspaceIdentity.ID)
	reception, _ = f.directory(t, ctx)
	require.Equal(t, new(domain.WorkspaceIdentityTypeUser), reception.HandlerType, "member reception")
	require.Equal(t, "成员", reception.HandlerName, "member reception")
	require.True(t, reception.Online, "member reception")
	require.Equal(t, domain.CustomerReceptionReplySoon, reception.Reply, "member reception")
	f.setWorkStatus(t, ctx, domain.WorkStatusAway, f.member.WorkspaceIdentity.ID)
	reception, _ = f.directory(t, ctx)
	require.Nil(t, reception.HandlerType, "away member reception")
}

// TestMessengerServiceSessionReception 验证访客会话按当前客服周期的队列与负责人推导接待状态。
func TestMessengerServiceSessionReception(t *testing.T) {
	t.Parallel()
	f := newExecutionScopeFixture(t)
	ctx := context.Background()
	f.setWorkStatus(t, ctx, domain.WorkStatusWorking)

	// 未认领的周期按所在队列展示。
	_, reception := f.directory(t, ctx)
	require.Nil(t, reception.HandlerType, "queued session reception")
	require.True(t, reception.Online, "queued session reception")
	require.Equal(t, domain.CustomerReceptionReplySoon, reception.Reply, "queued session reception")

	// 真人认领后展示负责人，不展示回复预期。
	_, err := f.claim.Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	_, reception = f.directory(t, ctx)
	require.Equal(t, new(domain.WorkspaceIdentityTypeUser), reception.HandlerType, "claimed session reception")
	require.Equal(t, f.owner.WorkspaceIdentity.DisplayName, reception.HandlerName, "claimed session reception")
	require.True(t, reception.Online, "claimed session reception")
	require.Equal(t, domain.CustomerReceptionReplyNone, reception.Reply, "claimed session reception")

	// 转交 AI 员工后立即回复。
	_, err = f.transfer.Execute(ctx, f.owner, servicesessionaction.TransferServiceSessionInput{
		ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.agentIdentityID,
	})
	require.NoError(t, err)
	_, reception = f.directory(t, ctx)
	require.Equal(t, new(domain.WorkspaceIdentityTypeAgent), reception.HandlerType, "agent session reception")
	require.Equal(t, domain.CustomerReceptionReplyImmediate, reception.Reply, "agent session reception")

	// 周期结束后保留负责人，不展示在线与回复预期。
	_, err = f.claim.Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	_, err = f.close.Execute(ctx, f.owner, f.conversationID)
	require.NoError(t, err)
	_, reception = f.directory(t, ctx)
	require.NotNil(t, reception.HandlerType, "closed session reception")
	require.Equal(t, f.owner.WorkspaceIdentity.DisplayName, reception.HandlerName, "closed session reception")
	require.False(t, reception.Online, "closed session reception")
	require.Equal(t, domain.CustomerReceptionReplyNone, reception.Reply, "closed session reception")
}

// TestMessengerReceptionTeamNotification 验证从成员编辑修改所属团队时，加入与移出都通知企业全部网站访客重新读取接待状态。
func TestMessengerReceptionTeamNotification(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	team, err := teamaction.NewCreateTeamAction(f.db).Execute(ctx, f.owner, teamaction.Input{Name: "接待通知团队"})
	require.NoError(t, err)
	feed := startRealtimeFeed(t, f.owner.Workspace.ID)
	update := useraction.NewUpdateUserAction(f.db, testServiceSessionReturner(f.db), testEnqueuer)
	for _, teamIDs := range [][]string{{team.ID}, nil} {
		_, err := update.Execute(ctx, f.owner, f.member.User.ID, useraction.UpdateInput{
			DisplayName: "成员", RoleID: f.member.User.RoleID, TeamIDs: teamIDs, HandlesServiceRequests: true, MaxServiceSessions: 10,
		})
		require.NoError(t, err)
		feed.expect(t, feed.reception())
	}
}

// TestMessengerQueueReplyEstimate 验证队列在线时按最近 7 天按工作时间计的真人首响中位数说明通常回复时长，样本不足、窗口外的样本和首响超过 4 小时时尽快回复。
func TestMessengerQueueReplyEstimate(t *testing.T) {
	t.Parallel()
	f := newExecutionScopeFixture(t)
	ctx := context.Background()
	f.setWorkStatus(t, ctx, domain.WorkStatusWorking)
	var serviceConversationID string
	require.NoError(t, f.db.NewSelect().Table("service_conversations").Column("id").Where("conversation_id = ?", f.conversationID).Scan(ctx, &serviceConversationID))
	// addSamples 在公共队列写入 count 个已关闭周期，需要真人的时间在 age 之前，按工作时间计的首响为 wait。
	sequence := 100
	addSamples := func(count int, age, wait time.Duration) {
		for range count {
			sequence++
			requestedAt := time.Now().UTC().Add(-age)
			_, err := f.db.NewInsert().Model(&servermodels.ServiceSession{
				WorkspaceID: f.owner.Workspace.ID, ConversationID: f.conversationID, ServiceConversationID: serviceConversationID,
				Sequence: int64(sequence), Status: string(domain.ServiceSessionStatusClosed),
				OpeningMessageID: uuid.NewV7().String(), LastMessageID: uuid.NewV7().String(), LastMessageAt: requestedAt, StatusChangedAt: requestedAt,
				HumanRequestedAt: &requestedAt, HumanFirstResponseAt: new(requestedAt.Add(wait)), HumanFirstResponseSec: new(int(wait / time.Second)),
			}).Column("workspace_id", "conversation_id", "service_conversation_id", "sequence", "status", "opening_message_id", "last_message_id",
				"last_message_at", "status_changed_at", "human_requested_at", "human_first_response_at", "human_first_response_seconds").
				Exec(ctx)
			require.NoError(t, err)
		}
	}

	// 窗口外的样本和不足 5 个的样本都不改变尽快回复。
	addSamples(4, time.Hour, 8*time.Minute)
	addSamples(3, 8*24*time.Hour, 8*time.Minute)
	reception, _ := f.directory(t, ctx)
	require.Equal(t, domain.CustomerReceptionReplySoon, reception.Reply, "insufficient samples reception")
	addSamples(1, time.Hour, 8*time.Minute)
	newSession, queued := f.directory(t, ctx)
	require.Equal(t, domain.CustomerReceptionReplyTenMinutes, newSession.Reply, "estimated new session reception")
	require.Equal(t, domain.CustomerReceptionReplyTenMinutes, queued.Reply, "estimated queued reception")
	// 首响中位数超过 4 小时时尽快回复。
	addSamples(10, time.Hour, 5*time.Hour)
	reception, _ = f.directory(t, ctx)
	require.Equal(t, domain.CustomerReceptionReplySoon, reception.Reply, "slow estimated reception")
	// 队列离线时仍尽快回复。
	f.setWorkStatus(t, ctx, domain.WorkStatusOffDuty)
	reception, _ = f.directory(t, ctx)
	require.False(t, reception.Online, "offline estimated reception")
	require.Equal(t, domain.CustomerReceptionReplySoon, reception.Reply, "offline estimated reception")
}

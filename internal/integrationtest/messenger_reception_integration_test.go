//go:build server

package integrationtest

import (
	"context"
	"testing"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	customerserviceaction "github.com/runforyou-ai/luway/internal/actions/customerservice"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	teamaction "github.com/runforyou-ai/luway/internal/actions/team"
	useraction "github.com/runforyou-ai/luway/internal/actions/user"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
	"uuid"
)

// messengerReceptionVisitor 是接待状态测试使用的访客渠道身份。
const messengerReceptionVisitor = "web-session:0123456789abcdef0123456789abcdef"

// directory 读取访客目录，返回新会话接待状态与测试会话的接待状态。
func (f executionScopeFixture) directory(t *testing.T, ctx context.Context) (chatstate.Reception, chatstate.Reception) {
	t.Helper()
	directory, err := customerchataction.NewListWebsiteConversationsQuery(f.db).Execute(ctx, f.channelID, messengerReceptionVisitor)
	if err != nil {
		t.Fatal(err)
	}
	for _, conversation := range directory.Conversations {
		if conversation.ID == f.conversationID {
			return directory.NewSessionReception, conversation.Reception
		}
	}
	t.Fatalf("visitor conversation missing: %+v", directory.Conversations)
	return chatstate.Reception{}, chatstate.Reception{}
}

// setWorkStatus 设置企业内全部真人成员或指定身份的工作状态。
func (f executionScopeFixture) setWorkStatus(t *testing.T, ctx context.Context, status domain.WorkStatus, identityIDs ...string) {
	t.Helper()
	query := f.db.NewUpdate().Model((*servermodels.OrganizationIdentity)(nil)).
		Set("work_status = ?", status).
		Where("organization_id = ? AND type = ?", f.owner.Organization.ID, domain.OrganizationIdentityTypeUser)
	if len(identityIDs) > 0 {
		query = query.Where("id IN (?)", bun.In(identityIDs))
	}
	if _, err := query.Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

// setInitialRoute 修改测试渠道的新会话路由，失败目标保持公共队列。
func (f executionScopeFixture) setInitialRoute(t *testing.T, ctx context.Context, targetType domain.ChannelRoutingTargetType, targetID *string) {
	t.Helper()
	if _, err := f.db.NewUpdate().Model((*servermodels.Channel)(nil)).
		Set("initial_routing_target_type = ?", targetType).
		Set("initial_routing_target_id = ?", targetID).
		Where("organization_id = ? AND id = ?", f.owner.Organization.ID, f.channelID).
		Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestMessengerNewSessionReception 验证网站新会话按当前路由、工作状态与工作时间推导接待状态。
func TestMessengerNewSessionReception(t *testing.T) {
	t.Parallel()
	f := newExecutionScopeFixture(t)
	ctx := context.Background()

	// 公共队列有工作中的接待成员时在线并尽快回复，全员下班时离线。
	f.setWorkStatus(t, ctx, domain.WorkStatusOffDuty)
	f.setWorkStatus(t, ctx, domain.WorkStatusWorking, f.member.OrganizationIdentity.ID)
	if reception, _ := f.directory(t, ctx); reception.HandlerType != nil || !reception.Online || reception.Reply != domain.CustomerReceptionReplySoon {
		t.Fatalf("queue reception = %+v", reception)
	}
	f.setWorkStatus(t, ctx, domain.WorkStatusOffDuty)
	if reception, _ := f.directory(t, ctx); reception.Online || reception.Reply != domain.CustomerReceptionReplySoon {
		t.Fatalf("offline queue reception = %+v", reception)
	}

	// 工作时间外按下个工作时段回复。
	now := time.Now().UTC()
	hours := domain.BusinessHours{Enabled: true, TimeZone: "UTC", Overrides: []domain.BusinessHoursOverride{{Date: now.Format(domain.BusinessHoursDateLayout)}}}
	for day := range hours.Weekly {
		hours.Weekly[day] = []domain.BusinessHoursPeriod{{Start: "00:00", End: "24:00"}}
	}
	if _, err := customerserviceaction.NewUpdateBusinessHoursAction(f.db).Execute(ctx, f.owner, hours); err != nil {
		t.Fatal(err)
	}
	f.setWorkStatus(t, ctx, domain.WorkStatusWorking)
	tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	if reception, _ := f.directory(t, ctx); reception.Online || reception.Reply != domain.CustomerReceptionReplyScheduled || reception.NextOpeningAt == nil || !reception.NextOpeningAt.Equal(tomorrow) {
		t.Fatalf("after hours reception = %+v", reception)
	}
	// 目录同时给出工作时间开关的下一时刻，访客端到时重新读取。
	if directory, err := customerchataction.NewListWebsiteConversationsQuery(f.db).Execute(ctx, f.channelID, messengerReceptionVisitor); err != nil ||
		directory.ReceptionRefreshAt == nil || !directory.ReceptionRefreshAt.Equal(tomorrow) {
		t.Fatalf("reception refresh at = %v, err = %v", directory.ReceptionRefreshAt, err)
	}
	hours.Enabled = false
	if _, err := customerserviceaction.NewUpdateBusinessHoursAction(f.db).Execute(ctx, f.owner, hours); err != nil {
		t.Fatal(err)
	}

	// AI 员工首接待时始终在线并立即回复。
	f.setInitialRoute(t, ctx, domain.ChannelRoutingTargetTypeMember, &f.agentIdentityID)
	if reception, _ := f.directory(t, ctx); reception.HandlerType == nil || *reception.HandlerType != domain.OrganizationIdentityTypeAgent ||
		reception.HandlerName != "执行范围助手" || !reception.Online || reception.Reply != domain.CustomerReceptionReplyImmediate {
		t.Fatalf("agent reception = %+v", reception)
	}

	// 真人首接待工作中时展示该成员，不在工作中时顺延到公共队列。
	f.setInitialRoute(t, ctx, domain.ChannelRoutingTargetTypeMember, &f.member.OrganizationIdentity.ID)
	if reception, _ := f.directory(t, ctx); reception.HandlerType == nil || *reception.HandlerType != domain.OrganizationIdentityTypeUser ||
		reception.HandlerName != "成员" || !reception.Online || reception.Reply != domain.CustomerReceptionReplySoon {
		t.Fatalf("member reception = %+v", reception)
	}
	f.setWorkStatus(t, ctx, domain.WorkStatusAway, f.member.OrganizationIdentity.ID)
	if reception, _ := f.directory(t, ctx); reception.HandlerType != nil {
		t.Fatalf("away member reception = %+v", reception)
	}
}

// TestMessengerServiceSessionReception 验证访客会话按当前客服周期的队列与负责人推导接待状态。
func TestMessengerServiceSessionReception(t *testing.T) {
	t.Parallel()
	f := newExecutionScopeFixture(t)
	ctx := context.Background()
	f.setWorkStatus(t, ctx, domain.WorkStatusWorking)

	// 未认领的周期按所在队列展示。
	if _, reception := f.directory(t, ctx); reception.HandlerType != nil || !reception.Online || reception.Reply != domain.CustomerReceptionReplySoon {
		t.Fatalf("queued session reception = %+v", reception)
	}

	// 真人认领后展示负责人，不展示回复预期。
	if _, err := f.claim.Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if _, reception := f.directory(t, ctx); reception.HandlerType == nil || *reception.HandlerType != domain.OrganizationIdentityTypeUser ||
		reception.HandlerName != f.owner.OrganizationIdentity.DisplayName || !reception.Online || reception.Reply != domain.CustomerReceptionReplyNone {
		t.Fatalf("claimed session reception = %+v", reception)
	}

	// 转交 AI 员工后立即回复。
	if _, err := f.transfer.Execute(ctx, f.owner, servicesessionaction.TransferServiceSessionInput{
		ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.agentIdentityID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, reception := f.directory(t, ctx); reception.HandlerType == nil || *reception.HandlerType != domain.OrganizationIdentityTypeAgent || reception.Reply != domain.CustomerReceptionReplyImmediate {
		t.Fatalf("agent session reception = %+v", reception)
	}

	// 周期结束后保留负责人，不展示在线与回复预期。
	if _, err := f.claim.Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.close.Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if _, reception := f.directory(t, ctx); reception.HandlerType == nil || reception.HandlerName != f.owner.OrganizationIdentity.DisplayName || reception.Online || reception.Reply != domain.CustomerReceptionReplyNone {
		t.Fatalf("closed session reception = %+v", reception)
	}
}

// TestMessengerReceptionTeamNotification 验证从成员编辑修改所属团队时，加入与移出都通知企业全部网站访客重新读取接待状态。
func TestMessengerReceptionTeamNotification(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	team, err := teamaction.NewCreateTeamAction(f.db).Execute(ctx, f.owner, teamaction.Input{Name: "接待通知团队"})
	if err != nil {
		t.Fatal(err)
	}
	feed := startRealtimeFeed(t, f.owner.Organization.ID)
	update := useraction.NewUpdateUserAction(f.db, testServiceSessionReturner(f.db), newTestTasks(f.db))
	for _, teamIDs := range [][]string{{team.ID}, nil} {
		if _, err := update.Execute(ctx, f.owner, f.member.User.ID, useraction.UpdateInput{
			DisplayName: "成员", RoleID: f.member.User.RoleID, TeamIDs: teamIDs, HandlesServiceRequests: true, MaxServiceSessions: 10,
		}); err != nil {
			t.Fatal(err)
		}
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
	if err := f.db.NewSelect().Table("service_conversations").Column("id").Where("conversation_id = ?", f.conversationID).Scan(ctx, &serviceConversationID); err != nil {
		t.Fatal(err)
	}
	// addSamples 在公共队列写入 count 个已关闭周期，需要真人的时间在 age 之前，按工作时间计的首响为 wait。
	sequence := 100
	addSamples := func(count int, age, wait time.Duration) {
		for range count {
			sequence++
			requestedAt := time.Now().UTC().Add(-age)
			if _, err := f.db.NewInsert().Model(&servermodels.ServiceSession{
				OrganizationID: f.owner.Organization.ID, ConversationID: f.conversationID, ServiceConversationID: serviceConversationID,
				Sequence: int64(sequence), Status: string(domain.ServiceSessionStatusClosed),
				OpeningMessageID: uuid.NewV7().String(), LastMessageID: uuid.NewV7().String(), LastMessageAt: requestedAt, StatusChangedAt: requestedAt,
				HumanRequestedAt: &requestedAt, HumanFirstResponseAt: new(requestedAt.Add(wait)), HumanFirstResponseSec: new(int(wait / time.Second)),
			}).Column("organization_id", "conversation_id", "service_conversation_id", "sequence", "status", "opening_message_id", "last_message_id",
				"last_message_at", "status_changed_at", "human_requested_at", "human_first_response_at", "human_first_response_seconds").
				Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}

	// 窗口外的样本和不足 5 个的样本都不改变尽快回复。
	addSamples(4, time.Hour, 8*time.Minute)
	addSamples(3, 8*24*time.Hour, 8*time.Minute)
	if reception, _ := f.directory(t, ctx); reception.Reply != domain.CustomerReceptionReplySoon {
		t.Fatalf("insufficient samples reception = %+v", reception)
	}
	addSamples(1, time.Hour, 8*time.Minute)
	newSession, queued := f.directory(t, ctx)
	if newSession.Reply != domain.CustomerReceptionReplyTenMinutes || queued.Reply != domain.CustomerReceptionReplyTenMinutes {
		t.Fatalf("estimated reception = %+v, %+v", newSession, queued)
	}
	// 首响中位数超过 4 小时时尽快回复。
	addSamples(10, time.Hour, 5*time.Hour)
	if reception, _ := f.directory(t, ctx); reception.Reply != domain.CustomerReceptionReplySoon {
		t.Fatalf("slow estimated reception = %+v", reception)
	}
	// 队列离线时仍尽快回复。
	f.setWorkStatus(t, ctx, domain.WorkStatusOffDuty)
	if reception, _ := f.directory(t, ctx); reception.Online || reception.Reply != domain.CustomerReceptionReplySoon {
		t.Fatalf("offline estimated reception = %+v", reception)
	}
}

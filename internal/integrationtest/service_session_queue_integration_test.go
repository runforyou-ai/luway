//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	agentrunaction "github.com/runforyou-ai/cervi/internal/actions/agentrun"
	channelaction "github.com/runforyou-ai/cervi/internal/actions/channel"
	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/cervi/internal/actions/customerchat"
	inboxaction "github.com/runforyou-ai/cervi/internal/actions/inbox"
	servicesessionaction "github.com/runforyou-ai/cervi/internal/actions/servicesession"
	teamaction "github.com/runforyou-ai/cervi/internal/actions/team"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// loadServiceSessionQueue 读取服务会话当前处理周期的负责人与所属队列。
func loadServiceSessionQueue(t *testing.T, f customerReadFixture, conversationID string) (*string, *string) {
	t.Helper()
	session := &servermodels.ServiceSession{}
	if err := f.db.NewSelect().Model(session).
		Join("JOIN service_conversations AS svc ON svc.current_service_session_id = ss.id AND svc.organization_id = ss.organization_id").
		Where("ss.organization_id = ? AND svc.conversation_id = ?", f.owner.Organization.ID, conversationID).
		Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return session.AssigneeIdentityID, session.TeamID
}

// queueConversationIDs 按队列筛选读取指定身份待领取条目中的会话编号与所属团队名称。
func queueConversationIDs(t *testing.T, f customerReadFixture, identity *servermodels.Identity, filter domain.ServiceQueueFilter, teamID string) map[string]*string {
	t.Helper()
	page, _, err := inboxaction.NewLoadInboxQuery(f.db).Execute(context.Background(), identity, inboxaction.LoadInput{
		Scope: domain.InboxScopePending, PendingKind: domain.InboxPendingKindQueue, QueueFilter: filter, QueueTeamID: teamID,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := make(map[string]*string, len(page.Conversations))
	for _, row := range page.Conversations {
		found[row.ID] = row.Service.TeamName
	}
	return found
}

// TestServiceSessionTeamQueue 验证转交到团队与公共队列、团队可用性判定、收件箱队列筛选和删除团队后的队列归属。
func TestServiceSessionTeamQueue(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	coordinator := newGroupAgentCoordinator(f.db)
	scheduler := agentrunaction.NewScheduler(newTestTasks(f.db))
	claim := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, newTestTasks(f.db))
	transfer := servicesessionaction.NewTransferServiceSessionAction(f.db, coordinator, scheduler, newTestTasks(f.db))
	createTeam := teamaction.NewCreateTeamAction(f.db)

	staffed, err := createTeam.Execute(ctx, f.owner, teamaction.Input{Name: "有人团队"})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := createTeam.Execute(ctx, f.owner, teamaction.Input{Name: "空团队"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := teamaction.NewAddMembersAction(f.db, newTestTasks(f.db)).Execute(ctx, f.owner, staffed.ID, []teamaction.MemberIdentity{
		{IdentityType: domain.OrganizationIdentityTypeUser, IdentityID: f.member.OrganizationIdentity.ID},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := claim.Execute(ctx, f.owner, f.conversationID); err != nil {
		t.Fatal(err)
	}
	_, err = transfer.Execute(ctx, f.owner, servicesessionaction.TransferServiceSessionInput{
		ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetTeam, TeamID: empty.ID,
	})
	var conflict *conversationaction.ConflictError
	if !errors.As(err, &conflict) || conflict.Reason != servicesessionaction.ConflictReasonTransferTeamUnavailable {
		t.Fatalf("转交给没有接待成员的团队 = %v", err)
	}

	if _, err := transfer.Execute(ctx, f.owner, servicesessionaction.TransferServiceSessionInput{
		ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetTeam, TeamID: staffed.ID,
	}); err != nil {
		t.Fatalf("转交给团队 = %v", err)
	}
	assignee, teamID := loadServiceSessionQueue(t, f, f.conversationID)
	if assignee != nil || teamID == nil || *teamID != staffed.ID {
		t.Fatalf("转交到团队队列后归属 = %v, %v", assignee, teamID)
	}

	if name, ok := queueConversationIDs(t, f, f.member, domain.ServiceQueueFilterTeam, staffed.ID)[f.conversationID]; !ok || name == nil || *name != staffed.Name {
		t.Fatalf("团队队列筛选缺少会话或团队名称 = %v", name)
	}
	if _, ok := queueConversationIDs(t, f, f.member, domain.ServiceQueueFilterPublic, "")[f.conversationID]; ok {
		t.Fatal("公共队列筛选不应包含团队队列中的会话")
	}
	if name, ok := queueConversationIDs(t, f, f.member, domain.ServiceQueueFilterAll, "")[f.conversationID]; !ok || name == nil {
		t.Fatalf("全部队列筛选缺少会话或团队标签 = %v", name)
	}
	// 待领取只包含公共队列和本人所在团队的队列。
	if _, ok := queueConversationIDs(t, f, f.owner, domain.ServiceQueueFilterAll, "")[f.conversationID]; ok {
		t.Fatal("非团队成员的待领取不应包含该团队队列中的会话")
	}

	// 领取与转交给个人都不改变队列。
	if _, err := claim.Execute(ctx, f.member, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if _, teamID := loadServiceSessionQueue(t, f, f.conversationID); teamID == nil || *teamID != staffed.ID {
		t.Fatalf("领取后队列 = %v", teamID)
	}
	if _, err := transfer.Execute(ctx, f.member, servicesessionaction.TransferServiceSessionInput{
		ConversationID: f.conversationID, TargetKind: domain.ServiceSessionTargetMember, IdentityID: f.owner.OrganizationIdentity.ID,
	}); err != nil {
		t.Fatalf("转交给个人 = %v", err)
	}
	assignee, teamID = loadServiceSessionQueue(t, f, f.conversationID)
	if assignee == nil || *assignee != f.owner.OrganizationIdentity.ID || teamID == nil || *teamID != staffed.ID {
		t.Fatalf("转交给个人后归属 = %v, %v", assignee, teamID)
	}

	// 渠道路由到没有接待成员的团队时降级到公共队列。
	updateChannel := channelaction.NewUpdateMessageChannelAction(f.db)
	route := func(target channelaction.RoutingTarget) {
		t.Helper()
		if _, err := updateChannel.ExecuteReception(ctx, f.owner, f.channelID, channelaction.MessageChannelReceptionInput{
			NewConversationTarget: target,
			FallbackTarget:        channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue},
		}); err != nil {
			t.Fatal(err)
		}
	}
	newSession := func(externalID string) string {
		t.Helper()
		result, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
			ChannelID: f.channelID, ExternalID: externalID, ClientMessageID: uuid.NewV7().String(), Body: "新访客消息",
		})
		if err != nil {
			t.Fatal(err)
		}
		return result.Conversation.ID
	}
	route(channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeTeam, ID: empty.ID})
	if _, teamID := loadServiceSessionQueue(t, f, newSession("web-session:11111111111111111111111111111111")); teamID != nil {
		t.Fatalf("路由到空团队应降级到公共队列，实际队列 = %v", *teamID)
	}
	route(channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypeTeam, ID: staffed.ID})
	teamConversationID := newSession("web-session:22222222222222222222222222222222")
	if _, teamID := loadServiceSessionQueue(t, f, teamConversationID); teamID == nil || *teamID != staffed.ID {
		t.Fatalf("路由到有接待成员的团队，实际队列 = %v", teamID)
	}

	// 删除团队后其队列中的处理周期并入公共队列。
	route(channelaction.RoutingTarget{Type: domain.ChannelRoutingTargetTypePublicQueue})
	if err := teamaction.NewDeleteTeamAction(f.db, newTestTasks(f.db)).Execute(ctx, f.owner, staffed.ID); err != nil {
		t.Fatal(err)
	}
	if _, teamID := loadServiceSessionQueue(t, f, teamConversationID); teamID != nil {
		t.Fatalf("删除团队后队列 = %v", *teamID)
	}
}

// TestDeleteTeamWaitsForTransfer 验证删除团队与转交给团队互斥：转交持有团队共享锁并写入队列后，删除等待其提交，仍把该周期并入公共队列。
func TestDeleteTeamWaitsForTransfer(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	team, err := teamaction.NewCreateTeamAction(f.db).Execute(ctx, f.owner, teamaction.Input{Name: "并发删除团队"})
	if err != nil {
		t.Fatal(err)
	}
	written, commit := make(chan string, 1), make(chan struct{})
	transferred := make(chan error, 1)
	go func() {
		transferred <- f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			// 与转交给团队相同的锁顺序：先对团队行取共享锁，再写入周期所属队列。
			var lockedID string
			if err := tx.NewSelect().TableExpr("teams AS t").Column("id").
				Where("t.organization_id = ? AND t.id = ?", f.owner.Organization.ID, team.ID).
				For("KEY SHARE").Scan(ctx, &lockedID); err != nil {
				return err
			}
			if _, err := tx.NewUpdate().Table("service_sessions").Set("team_id = ?", team.ID).
				Where("organization_id = ? AND conversation_id = ?", f.owner.Organization.ID, f.conversationID).
				Exec(ctx); err != nil {
				return err
			}
			var xid string
			if err := tx.NewSelect().ColumnExpr("pg_current_xact_id()::text").Scan(ctx, &xid); err != nil {
				return err
			}
			written <- xid
			<-commit
			return nil
		})
	}()

	xid := <-written
	deleted := make(chan error, 1)
	go func() {
		deleted <- teamaction.NewDeleteTeamAction(f.db, newTestTasks(f.db)).Execute(ctx, f.owner, team.ID)
	}()
	// 等删除在该事务上排队，确保队列清理发生在转交提交之后。
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := f.db.NewSelect().TableExpr("pg_locks").ColumnExpr("count(*)").
			Where("NOT granted AND locktype = 'transactionid' AND transactionid::text = ?", xid).
			Scan(ctx, &waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("删除团队没有等待转交事务")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(commit)
	if err := <-transferred; err != nil {
		t.Fatal(err)
	}
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	if _, teamID := loadServiceSessionQueue(t, f, f.conversationID); teamID != nil {
		t.Fatalf("删除团队后仍指向已删团队，队列 = %v", *teamID)
	}
}

//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	deviceaction "github.com/runforyou-ai/luway/internal/actions/device"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// TestGroupPersonalAgents 验证个人 AI 员工只能由负责人带进群、在群内被点名时派发到负责人电脑，并随负责人退群、被移出或停用而离开。
func TestGroupPersonalAgents(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	ctx := context.Background()
	member := newChatLockUser(t, db, identity)
	device, err := deviceaction.NewRegisterDeviceAction(db).Execute(ctx, member, deviceaction.RegisterInput{InstallID: uuid.NewV7().String(), Name: "成员电脑", Platform: domain.DevicePlatformMacOS})
	if err != nil {
		t.Fatal(err)
	}
	personalAgent, err := agentaction.NewCreatePersonalAgentAction(db).Execute(ctx, member, device.ID, agentaction.PersonalAgentInput{
		DisplayName: "成员个人 AI 员工", Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ProviderID: providerID, ModelIdentifier: modelID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	group, err := groupchataction.NewCreateGroupConversationAction(db).Execute(ctx, identity, groupchataction.GroupConversationInput{
		Title: "个人 AI 员工群", MemberIdentityIDs: []string{member.OrganizationIdentity.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	add := groupchataction.NewAddGroupConversationMembersAction(db)
	remove := groupchataction.NewRemoveGroupConversationMemberAction(db, newGroupAgentCoordinator(db))
	addPersonalAgent := func() {
		t.Helper()
		if _, err := add.Execute(ctx, member, groupchataction.GroupConversationMembersInput{ConversationID: group.ID, MemberIdentityIDs: []string{personalAgent.IdentityID}}); err != nil {
			t.Fatal(err)
		}
	}
	inGroup := func() bool {
		t.Helper()
		active, err := db.NewSelect().TableExpr("conversation_participants AS cp").
			Join("JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.organization_id = cp.organization_id").
			Where("cp.conversation_id = ? AND cs.source_id = ? AND cp.left_at IS NULL", group.ID, personalAgent.IdentityID).Exists(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return active
	}
	t.Run("只有负责人能带个人 AI 员工进群", func(t *testing.T) {
		if _, err := add.Execute(ctx, identity, groupchataction.GroupConversationMembersInput{ConversationID: group.ID, MemberIdentityIDs: []string{personalAgent.IdentityID}}); !errors.Is(err, conversationaction.ErrGroupMemberNotFound) {
			t.Fatalf("group owner added member personalAgent=%v", err)
		}
		// 非群主只能加入本人负责的个人 AI 员工。
		if _, err := add.Execute(ctx, member, groupchataction.GroupConversationMembersInput{ConversationID: group.ID, MemberIdentityIDs: []string{newChatLockUser(t, db, identity).OrganizationIdentity.ID}}); !errors.Is(err, chatstate.ErrGroupOwnerRequired) {
			t.Fatalf("member added colleague=%v", err)
		}
		addPersonalAgent()
		if !inGroup() {
			t.Fatal("personalAgent not in group")
		}
	})

	t.Run("群内点名派发到负责人电脑", func(t *testing.T) {
		subjectID := loadIdentitySubjectID(t, db, identity.Organization.ID, personalAgent.IdentityID)
		if _, err := newGroupSendAction(db).Execute(ctx, identity, groupchataction.GroupTextMessageInput{
			ConversationID: group.ID, ClientMessageID: uuid.NewV7().String(), Body: "请个人 AI 员工看看", MentionSubjectIDs: []string{subjectID},
		}); err != nil {
			t.Fatal(err)
		}
		var run servermodels.AgentRun
		if err := db.NewSelect().Model(&run).Where("agr.conversation_id = ? AND agr.agent_identity_id = ?", group.ID, personalAgent.IdentityID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if run.ExecutionDeviceID == nil || *run.ExecutionDeviceID != device.ID {
			t.Fatalf("group personalAgent run=%+v", run)
		}
		// 群成员与运行状态中的个人 AI 员工携带负责人名称，真人成员不携带。
		owner := member.OrganizationIdentity.DisplayName
		loaded, err := groupchataction.NewGetGroupConversationQuery(db).Execute(ctx, identity, group.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, participant := range loaded.Participants {
			if participant.IdentityID == personalAgent.IdentityID {
				if participant.PersonalResponsibleName == nil || *participant.PersonalResponsibleName != owner ||
					participant.PersonalResponsibleIdentityID == nil || *participant.PersonalResponsibleIdentityID != member.OrganizationIdentity.ID {
					t.Fatalf("group personalAgent owner=%+v", participant)
				}
			} else if participant.PersonalResponsibleName != nil || participant.PersonalResponsibleIdentityID != nil {
				t.Fatalf("group member owner name=%+v", participant)
			}
		}
		history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: group.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(history.AgentRuns) != 1 || history.AgentRuns[0].AgentPersonalResponsibleName == nil || *history.AgentRuns[0].AgentPersonalResponsibleName != owner {
			t.Fatalf("group personalAgent runs=%+v", history.AgentRuns)
		}
		// 个人 AI 员工入群事件的目标快照携带负责人名称。
		var joined *conversationaction.ConversationSystemEventParticipant
		for _, message := range history.Messages {
			if message.SystemEvent == nil || message.SystemEvent.Type != domain.ConversationSystemEventGroupMembersAdded {
				continue
			}
			for _, target := range message.SystemEvent.Targets {
				if target.IdentityID == personalAgent.IdentityID {
					joined = &target
				}
			}
		}
		if joined == nil || joined.PersonalResponsibleName == nil || *joined.PersonalResponsibleName != owner {
			t.Fatalf("personalAgent joined event target=%+v", joined)
		}
		// 暂停后群内点名在发送前被拒绝，不排队也不留下消息。
		if _, err := agentaction.NewSetPersonalAgentPausedAction(db).Execute(ctx, member, personalAgent.ID, true); err != nil {
			t.Fatal(err)
		}
		before, err := db.NewSelect().TableExpr("agent_inputs AS ai").Join("JOIN agent_lanes AS al ON al.id = ai.lane_id").Where("al.conversation_id = ?", group.ID).Count(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = newGroupSendAction(db).Execute(ctx, identity, groupchataction.GroupTextMessageInput{
			ConversationID: group.ID, ClientMessageID: uuid.NewV7().String(), Body: "暂停中", MentionSubjectIDs: []string{subjectID},
		})
		if conflict, ok := errors.AsType[*conversationaction.ConflictError](err); !ok || conflict.Reason != conversationaction.ConflictReasonPersonalAgentPaused {
			t.Fatalf("mention paused personalAgent=%v", err)
		}
		after, err := db.NewSelect().TableExpr("agent_inputs AS ai").Join("JOIN agent_lanes AS al ON al.id = ai.lane_id").Where("al.conversation_id = ?", group.ID).Count(ctx)
		if err != nil || after != before {
			t.Fatalf("paused personalAgent inputs=%d before=%d %v", after, before, err)
		}
		if _, err := agentaction.NewSetPersonalAgentPausedAction(db).Execute(ctx, member, personalAgent.ID, false); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("负责人移出自己的个人 AI 员工", func(t *testing.T) {
		if _, err := remove.Execute(ctx, member, groupchataction.GroupConversationMemberInput{ConversationID: group.ID, MemberIdentityID: personalAgent.IdentityID}); err != nil {
			t.Fatal(err)
		}
		if inGroup() {
			t.Fatal("personalAgent still in group")
		}
		addPersonalAgent()
	})

	t.Run("负责人退群时个人 AI 员工随之退出", func(t *testing.T) {
		if err := groupchataction.NewLeaveGroupConversationAction(db, newGroupAgentCoordinator(db)).Execute(ctx, member, group.ID); err != nil {
			t.Fatal(err)
		}
		if inGroup() {
			t.Fatal("personalAgent stayed after owner left")
		}
		if _, err := add.Execute(ctx, identity, groupchataction.GroupConversationMembersInput{ConversationID: group.ID, MemberIdentityIDs: []string{member.OrganizationIdentity.ID}}); err != nil {
			t.Fatal(err)
		}
		addPersonalAgent()
	})

	t.Run("负责人被移出时个人 AI 员工随之退出", func(t *testing.T) {
		if _, err := remove.Execute(ctx, identity, groupchataction.GroupConversationMemberInput{ConversationID: group.ID, MemberIdentityID: member.OrganizationIdentity.ID}); err != nil {
			t.Fatal(err)
		}
		if inGroup() {
			t.Fatal("personalAgent stayed after owner removed")
		}
		if _, err := add.Execute(ctx, identity, groupchataction.GroupConversationMembersInput{ConversationID: group.ID, MemberIdentityIDs: []string{member.OrganizationIdentity.ID}}); err != nil {
			t.Fatal(err)
		}
		addPersonalAgent()
	})

	t.Run("负责人停用时个人 AI 员工停用并退群", func(t *testing.T) {
		status := testUserStatusAction(db)
		if _, err := status.Execute(ctx, identity, member.User.ID, domain.IdentityStatusInactive); err != nil {
			t.Fatal(err)
		}
		if inGroup() {
			t.Fatal("personalAgent stayed after owner deactivated")
		}
		personalAgents, err := agentaction.NewListPersonalAgentsQuery(db).Execute(ctx, identity, member.User.ID)
		if err != nil || len(personalAgents) != 1 || personalAgents[0].Status != domain.IdentityStatusInactive {
			t.Fatalf("personalAgents after owner deactivated=%+v %v", personalAgents, err)
		}
		updateStatus := agentaction.NewUpdatePersonalAgentStatusAction(db)
		if _, err := updateStatus.Execute(ctx, identity, personalAgent.ID, domain.IdentityStatusActive); !errors.Is(err, agentaction.ErrPersonalAgentResponsibleInactive) {
			t.Fatalf("reactivate with inactive owner=%v", err)
		}
		// 负责人恢复后个人 AI 员工保持停用，由负责人重新启用。
		if _, err := status.Execute(ctx, identity, member.User.ID, domain.IdentityStatusActive); err != nil {
			t.Fatal(err)
		}
		personalAgents, err = agentaction.NewListPersonalAgentsQuery(db).Execute(ctx, identity, member.User.ID)
		if err != nil || personalAgents[0].Status != domain.IdentityStatusInactive {
			t.Fatalf("personalAgent after owner restored=%+v %v", personalAgents, err)
		}
		if _, err := updateStatus.Execute(ctx, member, personalAgent.ID, domain.IdentityStatusActive); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("启用个人 AI 员工与停用负责人并发", func(t *testing.T) {
		status := testUserStatusAction(db)
		updateStatus := agentaction.NewUpdatePersonalAgentStatusAction(db)
		for range 10 {
			if _, err := updateStatus.Execute(ctx, identity, personalAgent.ID, domain.IdentityStatusInactive); err != nil {
				t.Fatal(err)
			}
			errs := make(chan error, 2)
			go func() {
				_, err := updateStatus.Execute(ctx, identity, personalAgent.ID, domain.IdentityStatusActive)
				if errors.Is(err, agentaction.ErrPersonalAgentResponsibleInactive) {
					err = nil
				}
				errs <- err
			}()
			go func() {
				_, err := status.Execute(ctx, identity, member.User.ID, domain.IdentityStatusInactive)
				errs <- err
			}()
			for range 2 {
				if err := <-errs; err != nil {
					t.Fatalf("concurrent reactivate and owner deactivate=%v", err)
				}
			}
			// 负责人停用后个人 AI 员工不能保持启用。
			personalAgents, err := agentaction.NewListPersonalAgentsQuery(db).Execute(ctx, identity, member.User.ID)
			if err != nil || personalAgents[0].Status != domain.IdentityStatusInactive {
				t.Fatalf("personalAgent after owner deactivated concurrently=%+v %v", personalAgents, err)
			}
			if _, err := status.Execute(ctx, identity, member.User.ID, domain.IdentityStatusActive); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("成员互相启停对方个人 AI 员工不死锁", func(t *testing.T) {
		other := newChatLockUser(t, db, identity)
		otherDevice, err := deviceaction.NewRegisterDeviceAction(db).Execute(ctx, other, deviceaction.RegisterInput{InstallID: uuid.NewV7().String(), Name: "对方电脑", Platform: domain.DevicePlatformMacOS})
		if err != nil {
			t.Fatal(err)
		}
		otherPersonalAgent, err := agentaction.NewCreatePersonalAgentAction(db).Execute(ctx, other, otherDevice.ID, agentaction.PersonalAgentInput{
			DisplayName: "对方个人 AI 员工", Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ProviderID: providerID, ModelIdentifier: modelID}},
		})
		if err != nil {
			t.Fatal(err)
		}
		updateStatus := agentaction.NewUpdatePersonalAgentStatusAction(db)
		for i := range 10 {
			status := domain.IdentityStatusInactive
			if i%2 == 1 {
				status = domain.IdentityStatusActive
			}
			errs := make(chan error, 2)
			go func() {
				_, err := updateStatus.Execute(ctx, member, otherPersonalAgent.ID, status)
				errs <- err
			}()
			go func() {
				_, err := updateStatus.Execute(ctx, other, personalAgent.ID, status)
				errs <- err
			}()
			for range 2 {
				if err := <-errs; err != nil {
					t.Fatalf("cross personalAgent status=%v", err)
				}
			}
		}
	})
}

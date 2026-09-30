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

// TestGroupAssistants 验证助理只能由主人带进群、在群内被点名时派发到主人电脑，并随主人退群、被移出或停用而离开。
func TestGroupAssistants(t *testing.T) {
	t.Parallel()
	db, identity, providerID, modelID := newAIWorkspace(t)
	ctx := context.Background()
	member := newChatLockUser(t, db, identity)
	device, err := deviceaction.NewRegisterDeviceAction(db).Execute(ctx, member, deviceaction.RegisterInput{InstallID: uuid.NewV7().String(), Name: "成员电脑", Platform: domain.DevicePlatformMacOS})
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := agentaction.NewCreateAssistantAction(db).Execute(ctx, member, device.ID, agentaction.AssistantInput{
		DisplayName: "成员助理", Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ProviderID: providerID, ModelIdentifier: modelID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	group, err := groupchataction.NewCreateGroupConversationAction(db).Execute(ctx, identity, groupchataction.GroupConversationInput{
		Title: "助理群", MemberIdentityIDs: []string{member.OrganizationIdentity.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	add := groupchataction.NewAddGroupConversationMembersAction(db)
	remove := groupchataction.NewRemoveGroupConversationMemberAction(db, newGroupAgentCoordinator(db))
	addAssistant := func() {
		t.Helper()
		if _, err := add.Execute(ctx, member, groupchataction.GroupConversationMembersInput{ConversationID: group.ID, MemberIdentityIDs: []string{assistant.IdentityID}}); err != nil {
			t.Fatal(err)
		}
	}
	inGroup := func() bool {
		t.Helper()
		active, err := db.NewSelect().TableExpr("conversation_participants AS cp").
			Join("JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.organization_id = cp.organization_id").
			Where("cp.conversation_id = ? AND cs.source_id = ? AND cp.left_at IS NULL", group.ID, assistant.IdentityID).Exists(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return active
	}
	t.Run("只有主人能带助理进群", func(t *testing.T) {
		if _, err := add.Execute(ctx, identity, groupchataction.GroupConversationMembersInput{ConversationID: group.ID, MemberIdentityIDs: []string{assistant.IdentityID}}); !errors.Is(err, conversationaction.ErrGroupMemberNotFound) {
			t.Fatalf("group owner added member assistant=%v", err)
		}
		// 非群主只能加入本人名下的助理。
		if _, err := add.Execute(ctx, member, groupchataction.GroupConversationMembersInput{ConversationID: group.ID, MemberIdentityIDs: []string{newChatLockUser(t, db, identity).OrganizationIdentity.ID}}); !errors.Is(err, chatstate.ErrGroupOwnerRequired) {
			t.Fatalf("member added colleague=%v", err)
		}
		addAssistant()
		if !inGroup() {
			t.Fatal("assistant not in group")
		}
	})

	t.Run("群内点名派发到主人电脑", func(t *testing.T) {
		subjectID := loadIdentitySubjectID(t, db, identity.Organization.ID, assistant.IdentityID)
		if _, err := newGroupSendAction(db).Execute(ctx, identity, groupchataction.GroupTextMessageInput{
			ConversationID: group.ID, ClientMessageID: uuid.NewV7().String(), Body: "请助理看看", MentionSubjectIDs: []string{subjectID},
		}); err != nil {
			t.Fatal(err)
		}
		var run servermodels.AgentRun
		if err := db.NewSelect().Model(&run).Where("agr.conversation_id = ? AND agr.agent_identity_id = ?", group.ID, assistant.IdentityID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if run.ExecutionDeviceID == nil || *run.ExecutionDeviceID != device.ID {
			t.Fatalf("group assistant run=%+v", run)
		}
		// 群成员与运行状态中的助理携带主人名称，真人成员不携带。
		owner := member.OrganizationIdentity.DisplayName
		loaded, err := groupchataction.NewGetGroupConversationQuery(db).Execute(ctx, identity, group.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, participant := range loaded.Participants {
			if participant.IdentityID == assistant.IdentityID {
				if participant.AssistantOwnerName == nil || *participant.AssistantOwnerName != owner ||
					participant.AssistantOwnerIdentityID == nil || *participant.AssistantOwnerIdentityID != member.OrganizationIdentity.ID {
					t.Fatalf("group assistant owner=%+v", participant)
				}
			} else if participant.AssistantOwnerName != nil || participant.AssistantOwnerIdentityID != nil {
				t.Fatalf("group member owner name=%+v", participant)
			}
		}
		history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: group.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(history.AgentRuns) != 1 || history.AgentRuns[0].AgentAssistantOwnerName == nil || *history.AgentRuns[0].AgentAssistantOwnerName != owner {
			t.Fatalf("group assistant runs=%+v", history.AgentRuns)
		}
		// 助理入群事件的目标快照携带主人名称。
		var joined *conversationaction.ConversationSystemEventParticipant
		for _, message := range history.Messages {
			if message.SystemEvent == nil || message.SystemEvent.Type != domain.ConversationSystemEventGroupMembersAdded {
				continue
			}
			for _, target := range message.SystemEvent.Targets {
				if target.IdentityID == assistant.IdentityID {
					joined = &target
				}
			}
		}
		if joined == nil || joined.AssistantOwnerName == nil || *joined.AssistantOwnerName != owner {
			t.Fatalf("assistant joined event target=%+v", joined)
		}
		// 暂停后群内点名在发送前被拒绝，不排队也不留下消息。
		if _, err := agentaction.NewSetAssistantPausedAction(db).Execute(ctx, member, assistant.ID, true); err != nil {
			t.Fatal(err)
		}
		before, err := db.NewSelect().TableExpr("agent_inputs AS ai").Join("JOIN agent_lanes AS al ON al.id = ai.lane_id").Where("al.conversation_id = ?", group.ID).Count(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = newGroupSendAction(db).Execute(ctx, identity, groupchataction.GroupTextMessageInput{
			ConversationID: group.ID, ClientMessageID: uuid.NewV7().String(), Body: "暂停中", MentionSubjectIDs: []string{subjectID},
		})
		if conflict, ok := errors.AsType[*conversationaction.ConflictError](err); !ok || conflict.Reason != conversationaction.ConflictReasonAssistantPaused {
			t.Fatalf("mention paused assistant=%v", err)
		}
		after, err := db.NewSelect().TableExpr("agent_inputs AS ai").Join("JOIN agent_lanes AS al ON al.id = ai.lane_id").Where("al.conversation_id = ?", group.ID).Count(ctx)
		if err != nil || after != before {
			t.Fatalf("paused assistant inputs=%d before=%d %v", after, before, err)
		}
		if _, err := agentaction.NewSetAssistantPausedAction(db).Execute(ctx, member, assistant.ID, false); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("主人移出自己的助理", func(t *testing.T) {
		if _, err := remove.Execute(ctx, member, groupchataction.GroupConversationMemberInput{ConversationID: group.ID, MemberIdentityID: assistant.IdentityID}); err != nil {
			t.Fatal(err)
		}
		if inGroup() {
			t.Fatal("assistant still in group")
		}
		addAssistant()
	})

	t.Run("主人退群时助理随之退出", func(t *testing.T) {
		if err := groupchataction.NewLeaveGroupConversationAction(db, newGroupAgentCoordinator(db)).Execute(ctx, member, group.ID); err != nil {
			t.Fatal(err)
		}
		if inGroup() {
			t.Fatal("assistant stayed after owner left")
		}
		if _, err := add.Execute(ctx, identity, groupchataction.GroupConversationMembersInput{ConversationID: group.ID, MemberIdentityIDs: []string{member.OrganizationIdentity.ID}}); err != nil {
			t.Fatal(err)
		}
		addAssistant()
	})

	t.Run("主人被移出时助理随之退出", func(t *testing.T) {
		if _, err := remove.Execute(ctx, identity, groupchataction.GroupConversationMemberInput{ConversationID: group.ID, MemberIdentityID: member.OrganizationIdentity.ID}); err != nil {
			t.Fatal(err)
		}
		if inGroup() {
			t.Fatal("assistant stayed after owner removed")
		}
		if _, err := add.Execute(ctx, identity, groupchataction.GroupConversationMembersInput{ConversationID: group.ID, MemberIdentityIDs: []string{member.OrganizationIdentity.ID}}); err != nil {
			t.Fatal(err)
		}
		addAssistant()
	})

	t.Run("主人停用时助理停用并退群", func(t *testing.T) {
		status := testUserStatusAction(db)
		if _, err := status.Execute(ctx, identity, member.User.ID, domain.IdentityStatusInactive); err != nil {
			t.Fatal(err)
		}
		if inGroup() {
			t.Fatal("assistant stayed after owner deactivated")
		}
		assistants, err := agentaction.NewListAssistantsQuery(db).Execute(ctx, identity, member.User.ID)
		if err != nil || len(assistants) != 1 || assistants[0].Status != domain.IdentityStatusInactive {
			t.Fatalf("assistants after owner deactivated=%+v %v", assistants, err)
		}
		updateStatus := agentaction.NewUpdateAssistantStatusAction(db)
		if _, err := updateStatus.Execute(ctx, identity, assistant.ID, domain.IdentityStatusActive); !errors.Is(err, agentaction.ErrAssistantOwnerInactive) {
			t.Fatalf("reactivate with inactive owner=%v", err)
		}
		// 主人恢复后助理保持停用，由主人重新启用。
		if _, err := status.Execute(ctx, identity, member.User.ID, domain.IdentityStatusActive); err != nil {
			t.Fatal(err)
		}
		assistants, err = agentaction.NewListAssistantsQuery(db).Execute(ctx, identity, member.User.ID)
		if err != nil || assistants[0].Status != domain.IdentityStatusInactive {
			t.Fatalf("assistant after owner restored=%+v %v", assistants, err)
		}
		if _, err := updateStatus.Execute(ctx, member, assistant.ID, domain.IdentityStatusActive); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("启用助理与停用主人并发", func(t *testing.T) {
		status := testUserStatusAction(db)
		updateStatus := agentaction.NewUpdateAssistantStatusAction(db)
		for range 10 {
			if _, err := updateStatus.Execute(ctx, identity, assistant.ID, domain.IdentityStatusInactive); err != nil {
				t.Fatal(err)
			}
			errs := make(chan error, 2)
			go func() {
				_, err := updateStatus.Execute(ctx, identity, assistant.ID, domain.IdentityStatusActive)
				if errors.Is(err, agentaction.ErrAssistantOwnerInactive) {
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
			// 主人停用后助理不能保持启用。
			assistants, err := agentaction.NewListAssistantsQuery(db).Execute(ctx, identity, member.User.ID)
			if err != nil || assistants[0].Status != domain.IdentityStatusInactive {
				t.Fatalf("assistant after owner deactivated concurrently=%+v %v", assistants, err)
			}
			if _, err := status.Execute(ctx, identity, member.User.ID, domain.IdentityStatusActive); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("成员互相启停对方助理不死锁", func(t *testing.T) {
		other := newChatLockUser(t, db, identity)
		otherDevice, err := deviceaction.NewRegisterDeviceAction(db).Execute(ctx, other, deviceaction.RegisterInput{InstallID: uuid.NewV7().String(), Name: "对方电脑", Platform: domain.DevicePlatformMacOS})
		if err != nil {
			t.Fatal(err)
		}
		otherAssistant, err := agentaction.NewCreateAssistantAction(db).Execute(ctx, other, otherDevice.ID, agentaction.AssistantInput{
			DisplayName: "对方助理", Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ProviderID: providerID, ModelIdentifier: modelID}},
		})
		if err != nil {
			t.Fatal(err)
		}
		updateStatus := agentaction.NewUpdateAssistantStatusAction(db)
		for i := range 10 {
			status := domain.IdentityStatusInactive
			if i%2 == 1 {
				status = domain.IdentityStatusActive
			}
			errs := make(chan error, 2)
			go func() {
				_, err := updateStatus.Execute(ctx, member, otherAssistant.ID, status)
				errs <- err
			}()
			go func() {
				_, err := updateStatus.Execute(ctx, other, assistant.ID, status)
				errs <- err
			}()
			for range 2 {
				if err := <-errs; err != nil {
					t.Fatalf("cross assistant status=%v", err)
				}
			}
		}
	})
}

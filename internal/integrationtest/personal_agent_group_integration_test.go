//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"time"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	computeraction "github.com/runforyou-ai/luway/internal/actions/computer"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestGroupPersonalAgents 验证个人 AI 员工只能由负责人带进群、在群内被点名时派发到负责人电脑，并随负责人退群、被移出或停用而离开。
func TestGroupPersonalAgents(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	member := newChatLockUser(t, db, identity)
	computer, err := computeraction.NewRegisterComputerAction(db).Execute(ctx, member, computeraction.RegisterInput{InstallID: uuid.NewV7().String(), Name: "成员电脑"})
	require.NoError(t, err)
	personalAgent, err := agentaction.NewCreatePersonalAgentAction(db).Execute(ctx, member, computer.Record.ID, agentaction.PersonalAgentInput{
		DisplayName: "成员个人 AI 员工", Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID}},
	})
	require.NoError(t, err)
	group, err := groupchataction.NewCreateGroupConversationAction(db).Execute(ctx, identity, groupchataction.GroupConversationInput{
		Title: "个人 AI 员工群", MemberIdentityIDs: []string{member.WorkspaceIdentity.ID},
	})
	require.NoError(t, err)
	add := groupchataction.NewAddGroupConversationMembersAction(db)
	remove := groupchataction.NewRemoveGroupConversationMemberAction(db, testEnqueuer, newGroupAgentCoordinator(db))
	addPersonalAgent := func() {
		t.Helper()
		_, err := add.Execute(ctx, member, groupchataction.GroupConversationMembersInput{ConversationID: group.ID, MemberIdentityIDs: []string{personalAgent.IdentityID}})
		require.NoError(t, err)
	}
	inGroup := func() bool {
		t.Helper()
		active, err := db.NewSelect().TableExpr("conversation_participants AS cp").
			Join("JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.workspace_id = cp.workspace_id").
			Where("cp.conversation_id = ? AND cs.source_id = ? AND cp.left_at IS NULL", group.ID, personalAgent.IdentityID).Exists(ctx)
		require.NoError(t, err)
		return active
	}
	// submitted 在个人 AI 员工于群内的运行上登记一条等待群主确认的操作，返回调用编号。
	submitted := func() string {
		t.Helper()
		return insertPendingDecision(t, ctx, db, group.ID, personalAgent.IdentityID, loadIdentitySubjectID(t, db, identity.Workspace.ID, identity.WorkspaceIdentity.ID))
	}
	t.Run("只有负责人能带个人 AI 员工进群", func(t *testing.T) {
		_, err := add.Execute(ctx, identity, groupchataction.GroupConversationMembersInput{ConversationID: group.ID, MemberIdentityIDs: []string{personalAgent.IdentityID}})
		require.ErrorIs(t, err, conversationaction.ErrGroupMemberNotFound, "group owner added member personalAgent")
		// 非群主只能加入本人负责的个人 AI 员工。
		_, err = add.Execute(ctx, member, groupchataction.GroupConversationMembersInput{ConversationID: group.ID, MemberIdentityIDs: []string{newChatLockUser(t, db, identity).WorkspaceIdentity.ID}})
		require.ErrorIs(t, err, chatstate.ErrGroupOwnerRequired, "member added colleague")
		addPersonalAgent()
		require.True(t, inGroup(), "personalAgent not in group")
	})

	t.Run("群内点名建立服务端运行", func(t *testing.T) {
		subjectID := loadIdentitySubjectID(t, db, identity.Workspace.ID, personalAgent.IdentityID)
		_, err := newGroupSendAction(db).Execute(ctx, identity, groupchataction.GroupTextMessageInput{
			ConversationID: group.ID, ClientMessageID: uuid.NewV7().String(), Body: "请个人 AI 员工看看", MentionSubjectIDs: []string{subjectID},
		})
		require.NoError(t, err)
		var run servermodels.AgentRun
		require.NoError(t, db.NewSelect().Model(&run).Where("agr.conversation_id = ? AND agr.agent_identity_id = ?", group.ID, personalAgent.IdentityID).Scan(ctx))
		runTasks := 0
		for _, task := range testEnqueuer.Queued(agentrunaction.RunActionName, identity.Workspace.ID) {
			if task.Options.IdempotencyKey == "agent:"+run.ID {
				runTasks++
			}
		}
		require.Equal(t, 1, runTasks, "group personalAgent run task")
		// 群成员与运行状态中的个人 AI 员工携带负责人名称，真人成员不携带。
		owner := member.WorkspaceIdentity.DisplayName
		loaded, err := groupchataction.NewGetGroupConversationQuery(db).Execute(ctx, identity, group.ID)
		require.NoError(t, err)
		for _, participant := range loaded.Participants {
			if participant.IdentityID == personalAgent.IdentityID {
				require.Equal(t, &owner, participant.PersonalResponsibleName, "group personalAgent owner")
				require.Equal(t, &member.WorkspaceIdentity.ID, participant.PersonalResponsibleIdentityID, "group personalAgent owner")
			} else {
				require.Nil(t, participant.PersonalResponsibleName, "group member owner name=%+v", participant)
				require.Nil(t, participant.PersonalResponsibleIdentityID, "group member owner name=%+v", participant)
			}
		}
		history, err := conversationaction.NewListConversationMessagesQuery(db).Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: group.ID})
		require.NoError(t, err)
		require.Len(t, history.AgentRuns, 1)
		require.Equal(t, &owner, history.AgentRuns[0].AgentPersonalResponsibleName)
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
		require.NotNil(t, joined, "personalAgent joined event target")
		require.Equal(t, &owner, joined.PersonalResponsibleName)
		// 暂停后群内点名在发送前被拒绝，不排队也不留下消息。
		_, err = agentaction.NewSetPersonalAgentPausedAction(db).Execute(ctx, member, personalAgent.ID, true)
		require.NoError(t, err)
		before, err := db.NewSelect().TableExpr("agent_inputs AS ai").Join("JOIN agent_lanes AS al ON al.id = ai.lane_id").Where("al.conversation_id = ?", group.ID).Count(ctx)
		require.NoError(t, err)
		_, err = newGroupSendAction(db).Execute(ctx, identity, groupchataction.GroupTextMessageInput{
			ConversationID: group.ID, ClientMessageID: uuid.NewV7().String(), Body: "暂停中", MentionSubjectIDs: []string{subjectID},
		})
		conflict, ok := errors.AsType[*conversationaction.ConflictError](err)
		require.True(t, ok, "mention paused personalAgent=%v", err)
		require.Equal(t, conversationaction.ConflictReasonPersonalAgentPaused, conflict.Reason)
		after, err := db.NewSelect().TableExpr("agent_inputs AS ai").Join("JOIN agent_lanes AS al ON al.id = ai.lane_id").Where("al.conversation_id = ?", group.ID).Count(ctx)
		require.NoError(t, err)
		require.Equal(t, before, after, "paused personalAgent inputs")
		_, err = agentaction.NewSetPersonalAgentPausedAction(db).Execute(ctx, member, personalAgent.ID, false)
		require.NoError(t, err)
	})

	t.Run("负责人移出自己的个人 AI 员工", func(t *testing.T) {
		callID := submitted()
		_, err := remove.Execute(ctx, member, groupchataction.GroupConversationMemberInput{ConversationID: group.ID, MemberIdentityID: personalAgent.IdentityID})
		require.NoError(t, err)
		require.False(t, inGroup(), "personalAgent still in group")
		require.Equal(t, domain.AgentToolCallCancelled, toolCallStatus(t, ctx, db, callID), "decision after personalAgent removed")
		addPersonalAgent()
	})

	t.Run("负责人退群时个人 AI 员工随之退出", func(t *testing.T) {
		callID := submitted()
		require.NoError(t, groupchataction.NewLeaveGroupConversationAction(db, testEnqueuer, newGroupAgentCoordinator(db)).Execute(ctx, member, group.ID))
		require.False(t, inGroup(), "personalAgent stayed after owner left")
		require.Equal(t, domain.AgentToolCallCancelled, toolCallStatus(t, ctx, db, callID), "decision after owner left")
		_, err := add.Execute(ctx, identity, groupchataction.GroupConversationMembersInput{ConversationID: group.ID, MemberIdentityIDs: []string{member.WorkspaceIdentity.ID}})
		require.NoError(t, err)
		addPersonalAgent()
	})

	t.Run("负责人被移出时个人 AI 员工随之退出", func(t *testing.T) {
		callID := submitted()
		_, err := remove.Execute(ctx, identity, groupchataction.GroupConversationMemberInput{ConversationID: group.ID, MemberIdentityID: member.WorkspaceIdentity.ID})
		require.NoError(t, err)
		require.False(t, inGroup(), "personalAgent stayed after owner removed")
		require.Equal(t, domain.AgentToolCallCancelled, toolCallStatus(t, ctx, db, callID), "decision after owner removed")
		_, err = add.Execute(ctx, identity, groupchataction.GroupConversationMembersInput{ConversationID: group.ID, MemberIdentityIDs: []string{member.WorkspaceIdentity.ID}})
		require.NoError(t, err)
		addPersonalAgent()
	})

	t.Run("负责人停用时个人 AI 员工停用并退群", func(t *testing.T) {
		callID := submitted()
		status := testUserStatusAction(db)
		_, err := status.Execute(ctx, identity, member.User.ID, domain.IdentityStatusInactive)
		require.NoError(t, err)
		require.False(t, inGroup(), "personalAgent stayed after owner deactivated")
		require.Equal(t, domain.AgentToolCallCancelled, toolCallStatus(t, ctx, db, callID), "decision after owner deactivated")
		personalAgents, err := agentaction.NewListPersonalAgentsQuery(db).Execute(ctx, identity, member.User.ID)
		require.NoError(t, err)
		require.Len(t, personalAgents, 1)
		require.Equal(t, domain.IdentityStatusInactive, personalAgents[0].Status, "personalAgents after owner deactivated")
		updateStatus := agentaction.NewUpdatePersonalAgentStatusAction(db, testEnqueuer)
		_, err = updateStatus.Execute(ctx, identity, personalAgent.ID, domain.IdentityStatusActive)
		require.ErrorIs(t, err, agentaction.ErrPersonalAgentResponsibleInactive, "reactivate with inactive owner")
		// 负责人恢复后个人 AI 员工保持停用，由负责人重新启用。
		_, err = status.Execute(ctx, identity, member.User.ID, domain.IdentityStatusActive)
		require.NoError(t, err)
		personalAgents, err = agentaction.NewListPersonalAgentsQuery(db).Execute(ctx, identity, member.User.ID)
		require.NoError(t, err)
		require.Equal(t, domain.IdentityStatusInactive, personalAgents[0].Status, "personalAgent after owner restored")
		_, err = updateStatus.Execute(ctx, member, personalAgent.ID, domain.IdentityStatusActive)
		require.NoError(t, err)
	})

	t.Run("启用个人 AI 员工与停用负责人并发", func(t *testing.T) {
		status := testUserStatusAction(db)
		updateStatus := agentaction.NewUpdatePersonalAgentStatusAction(db, testEnqueuer)
		for range 10 {
			_, err := updateStatus.Execute(ctx, identity, personalAgent.ID, domain.IdentityStatusInactive)
			require.NoError(t, err)
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
				require.NoError(t, <-errs, "concurrent reactivate and owner deactivate")
			}
			// 负责人停用后个人 AI 员工不能保持启用。
			personalAgents, err := agentaction.NewListPersonalAgentsQuery(db).Execute(ctx, identity, member.User.ID)
			require.NoError(t, err)
			require.Equal(t, domain.IdentityStatusInactive, personalAgents[0].Status, "personalAgent after owner deactivated concurrently")
			_, err = status.Execute(ctx, identity, member.User.ID, domain.IdentityStatusActive)
			require.NoError(t, err)
		}
	})

	t.Run("成员互相启停对方个人 AI 员工不死锁", func(t *testing.T) {
		other := newChatLockUser(t, db, identity)
		otherComputer, err := computeraction.NewRegisterComputerAction(db).Execute(ctx, other, computeraction.RegisterInput{InstallID: uuid.NewV7().String(), Name: "对方电脑"})
		require.NoError(t, err)
		otherPersonalAgent, err := agentaction.NewCreatePersonalAgentAction(db).Execute(ctx, other, otherComputer.Record.ID, agentaction.PersonalAgentInput{
			DisplayName: "对方个人 AI 员工", Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{ModelID: modelID}},
		})
		require.NoError(t, err)
		updateStatus := agentaction.NewUpdatePersonalAgentStatusAction(db, testEnqueuer)
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
				require.NoError(t, <-errs, "cross personalAgent status")
			}
		}
	})
}

// insertPendingDecision 在 AI 员工于会话中最近的运行上插入一条等待指定聊天主体确认的工具调用，返回调用编号。
func insertPendingDecision(t *testing.T, ctx context.Context, db *bun.DB, conversationID, agentIdentityID, assigneeSubjectID string) string {
	t.Helper()
	var run servermodels.AgentRun
	require.NoError(t, db.NewSelect().Model(&run).Where("agr.conversation_id = ? AND agr.agent_identity_id = ?", conversationID, agentIdentityID).
		OrderExpr("agr.created_at DESC").Limit(1).Scan(ctx))
	intervention := string(domain.ToolInterventionConfirmation)
	expiresAt := time.Now().Add(time.Hour)
	call := &servermodels.AgentToolCall{
		ID: uuid.NewV7().String(), WorkspaceID: run.WorkspaceID, AgentRunID: run.ID, ModelCallID: uuid.NewV7().String(), ProviderCallID: "call-" + uuid.NewV7().String(),
		Name: "update_note", Source: string(domain.AgentToolSourceBusinessSystem), Arguments: "{}", SideEffects: true,
		Status: string(domain.AgentToolCallAwaitingDecision), Intervention: &intervention, AssigneeSubjectID: &assigneeSubjectID, ExpiresAt: &expiresAt,
	}
	_, err := db.NewInsert().Model(call).Exec(ctx)
	require.NoError(t, err)
	return call.ID
}

// toolCallStatus 读取工具调用的当前状态。
func toolCallStatus(t *testing.T, ctx context.Context, db *bun.DB, callID string) domain.AgentToolCallStatus {
	t.Helper()
	var status domain.AgentToolCallStatus
	require.NoError(t, db.NewSelect().Model((*servermodels.AgentToolCall)(nil)).Column("status").Where("id = ?", callID).Scan(ctx, &status))
	return status
}

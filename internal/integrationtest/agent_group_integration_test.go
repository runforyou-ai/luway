//go:build server

package integrationtest

import (
	"context"
	"fmt"
	"testing"
	"uuid"

	agentaction "github.com/runforyou-ai/luway/internal/actions/agent"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// TestGroupAgentMembership 验证活跃 Agent 建群、添加、移除和群主边界。
func TestGroupAgentMembership(t *testing.T) {
	t.Parallel()
	db, identity, _, modelID := newAIWorkspace(t)
	ctx := context.Background()
	agents := make([]*agentaction.Agent, 0, 2)
	for i := range 2 {
		agent, err := agentaction.NewCreateAgentAction(db).Execute(ctx, identity, agentaction.CreateInput{
			DisplayName: fmt.Sprintf("群成员助手 %d", i),
			Execution: agentaction.ExecutionInput{Mode: domain.AgentExecutionModeManaged, Managed: &agentaction.ManagedExecutionInput{
				ModelID: modelID, SystemInstruction: "协助企业成员",
			}},
		})
		require.NoError(t, err)
		agents = append(agents, agent)
	}
	group, err := groupchataction.NewCreateGroupConversationAction(db).Execute(ctx, identity, groupchataction.GroupConversationInput{
		Title: "AI 成员群", MemberIdentityIDs: []string{agents[0].IdentityID},
	})
	require.NoError(t, err)
	add := groupchataction.NewAddGroupConversationMembersAction(db)
	detail, err := add.Execute(ctx, identity, groupchataction.GroupConversationMembersInput{
		ConversationID: group.ID, MemberIdentityIDs: []string{agents[1].IdentityID},
	})
	require.NoError(t, err)
	require.Len(t, detail.Participants, 3)
	var agentSubjectID string
	for _, member := range detail.Participants {
		if member.IdentityID == identity.WorkspaceIdentity.ID {
			require.Equal(t, domain.WorkspaceIdentityTypeUser, member.IdentityType, "owner=%+v", member)
			require.Equal(t, domain.ConversationParticipantRoleOwner, member.Role, "owner=%+v", member)
		} else {
			require.Equal(t, domain.WorkspaceIdentityTypeAgent, member.IdentityType, "agent member=%+v", member)
			require.Equal(t, domain.ConversationParticipantRoleMember, member.Role, "agent member=%+v", member)
			if member.IdentityID == agents[0].IdentityID {
				agentSubjectID = member.ChatSubjectID
			}
		}
	}
	testGroupAgentMessages(t, db, identity, group.ID, agentSubjectID)
	testGroupAgentEligibility(t, db, identity, group.ID, agents[1])
	_, err = groupchataction.NewTransferGroupConversationOwnerAction(db).Execute(ctx, identity, groupchataction.GroupConversationOwnerInput{
		ConversationID: group.ID, OwnerIdentityID: agents[0].IdentityID,
	})
	var conflict *conversationaction.ConflictError
	require.ErrorAs(t, err, &conflict, "agent owner error")
	require.Equal(t, groupchataction.ConflictReasonGroupMemberNotActive, conflict.Reason)
	// 最后一位真人可直接解散含有 Agent 的群聊。
	_, err = groupchataction.NewDissolveGroupConversationAction(db, newGroupAgentCoordinator(db)).Execute(ctx, identity, group.ID)
	require.NoError(t, err)
	var stored servermodels.Conversation
	require.NoError(t, db.NewSelect().Model(&stored).Where("cv.id = ?", group.ID).Scan(ctx))
	require.Equal(t, string(domain.ConversationStatusArchived), stored.Status)
}

// testGroupAgentMessages 验证群内提醒接受 AI 员工，普通消息与 @所有人 不触发执行。
func testGroupAgentMessages(t *testing.T, db *bun.DB, identity *servermodels.Identity, groupID, agentSubjectID string) {
	ctx := context.Background()
	send := newGroupSendAction(db)
	mentioned, err := send.Execute(ctx, identity, groupchataction.GroupTextMessageInput{
		ConversationID: groupID, ClientMessageID: uuid.NewV7().String(), Body: "提醒 Agent", MentionSubjectIDs: []string{agentSubjectID},
	})
	require.NoError(t, err, "agent mention")
	require.Len(t, mentioned.Mentions, 1)
	require.Equal(t, agentSubjectID, mentioned.Mentions[0].ChatSubjectID)
	inputCount := func() int {
		t.Helper()
		count, err := db.NewSelect().TableExpr("agent_inputs AS ai").
			Join("JOIN agent_lanes AS al ON al.id = ai.lane_id").
			Where("al.conversation_id = ?", groupID).Count(ctx)
		require.NoError(t, err)
		return int(count)
	}
	require.Equal(t, 1, inputCount(), "点名后的输入数量")
	for _, all := range []bool{false, true} {
		_, err := send.Execute(ctx, identity, groupchataction.GroupTextMessageInput{
			ConversationID: groupID, ClientMessageID: uuid.NewV7().String(), Body: "群内消息", MentionAll: all,
		})
		require.NoError(t, err)
	}
	require.Equal(t, 1, inputCount(), "普通消息与 @所有人 追加了输入")
	runCount, err := db.NewSelect().Table("agent_runs").Where("conversation_id = ?", groupID).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), runCount, "群内运行数量")
}

// testGroupAgentEligibility 验证重新添加、停用状态和企业隔离。
func testGroupAgentEligibility(t *testing.T, db *bun.DB, identity *servermodels.Identity, groupID string, agent *agentaction.Agent) {
	ctx := context.Background()
	remove := groupchataction.NewRemoveGroupConversationMemberAction(db, testEnqueuer, newGroupAgentCoordinator(db))
	add := groupchataction.NewAddGroupConversationMembersAction(db)
	for _, active := range []bool{true, false} {
		_, err := remove.Execute(ctx, identity, groupchataction.GroupConversationMemberInput{ConversationID: groupID, MemberIdentityID: agent.IdentityID})
		require.NoError(t, err)
		if !active {
			_, err := agentaction.NewUpdateStatusAction(db, testEnqueuer, testServiceSessionReturner(db)).Execute(ctx, identity, agent.ID, domain.IdentityStatusInactive)
			require.NoError(t, err)
		}
		_, err = add.Execute(ctx, identity, groupchataction.GroupConversationMembersInput{ConversationID: groupID, MemberIdentityIDs: []string{agent.IdentityID}})
		if active {
			require.NoError(t, err)
		}
		if !active {
			require.ErrorIs(t, err, conversationaction.ErrGroupMemberNotFound, "inactive add error")
		}
	}
	_, err := groupchataction.NewCreateGroupConversationAction(db).Execute(ctx, identity, groupchataction.GroupConversationInput{
		Title: "停用 Agent", MemberIdentityIDs: []string{agent.IdentityID},
	})
	require.ErrorIs(t, err, conversationaction.ErrGroupMemberNotFound, "inactive create")
	_, err = agentaction.NewUpdateStatusAction(db, testEnqueuer, testServiceSessionReturner(db)).Execute(ctx, identity, agent.ID, domain.IdentityStatusActive)
	require.NoError(t, err)
	foreign := newNavigationFixture(t)
	_, err = groupchataction.NewCreateGroupConversationAction(db).Execute(ctx, foreign.owner, groupchataction.GroupConversationInput{
		Title: "跨企业 Agent", MemberIdentityIDs: []string{agent.IdentityID},
	})
	require.ErrorIs(t, err, conversationaction.ErrGroupMemberNotFound, "foreign create")
	_, err = add.Execute(ctx, foreign.owner, groupchataction.GroupConversationMembersInput{ConversationID: foreign.groupID, MemberIdentityIDs: []string{agent.IdentityID}})
	require.ErrorIs(t, err, conversationaction.ErrGroupMemberNotFound, "foreign add")
}

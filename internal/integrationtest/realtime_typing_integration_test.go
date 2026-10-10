//go:build server

package integrationtest

import (
	"context"
	"net/http"
	"testing"
	"time"
	"uuid"

	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
	"github.com/stretchr/testify/require"
)

// nextTyping 在时限内读取下一条输入状态事件，跳过心跳与变更通知。
func (c *realtimeTestClient) nextTyping() protocol.ConversationTyping {
	c.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case frame, ok := <-c.frames:
			if !ok {
				c.t.Fatal("事件流已结束")
			}
			if typing, matched := frame.(protocol.ConversationTyping); matched {
				return typing
			}
		case <-timeout:
			c.t.Fatal("等待输入状态事件超时")
		}
	}
}

// expectNoTyping 在短暂等待内确认事件流没有输入状态事件，其他事件忽略。
func (c *realtimeTestClient) expectNoTyping() {
	c.t.Helper()
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case frame, ok := <-c.frames:
			if !ok {
				c.t.Fatal("事件流已结束")
			}
			if _, matched := frame.(protocol.ConversationTyping); matched {
				c.t.Fatalf("收到不应送达的输入状态 %#v", frame)
			}
		case <-timeout:
			return
		}
	}
}

// TestRealtimeConversationTyping 验证单聊与群聊输入状态只送达其他有效真人成员，本人其他设备与退群成员收不到，无资格上报返回会话不存在。
func TestRealtimeConversationTyping(t *testing.T) {
	t.Parallel()
	f := newNavigationFixture(t)
	ctx := context.Background()
	workspaceID := f.owner.Workspace.ID
	h := startRealtimeGateway(t, f)
	ownerToken := loginToken(t, f.db, workspaceID, f.owner.Account.Email)
	memberToken := loginToken(t, f.db, workspaceID, f.member.Account.Email)
	ownerOther, _ := h.connect(t, loginToken(t, f.db, workspaceID, f.owner.Account.Email))
	member, _ := h.connect(t, memberToken)
	var ownerSubjectID string
	require.NoError(t, f.db.NewSelect().Table("chat_subjects").Column("id").
		Where("workspace_id = ? AND kind = ? AND source_id = ?", workspaceID, domain.ChatSubjectKindWorkspaceIdentity, f.owner.WorkspaceIdentity.ID).
		Scan(ctx, &ownerSubjectID))
	report := func(token, conversationID string, active bool) error {
		return h.backend.ReportConversationTyping(ctx, appservice.RequestMeta{Token: token, WorkspaceID: workspaceID}, conversationID, appservice.ConversationTypingInput{Active: active})
	}

	// 群主输入送达群成员，群主其他设备不收到本人输入状态。
	require.NoError(t, report(ownerToken, f.groupID, true))
	require.Equal(t, protocol.ConversationTyping{ConversationID: f.groupID, SenderSubjectID: ownerSubjectID, Active: true}, member.nextTyping())
	require.NoError(t, report(ownerToken, f.groupID, false))
	stopped := member.nextTyping()
	require.False(t, stopped.Active)
	require.Equal(t, ownerSubjectID, stopped.SenderSubjectID)
	ownerOther.expectNoTyping()

	// 单聊输入送达对方。
	direct, err := directchataction.NewSendFirstDirectTextMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{TargetIdentityID: f.member.WorkspaceIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "单聊"})
	require.NoError(t, err)
	require.NoError(t, report(memberToken, direct.Conversation.ID, true))
	require.Equal(t, protocol.ConversationTyping{ConversationID: direct.Conversation.ID, SenderSubjectID: f.subjectID, Active: true}, ownerOther.nextTyping())

	// 退出群聊后无法上报，也不再收到该群的输入状态。
	require.NoError(t, groupchataction.NewLeaveGroupConversationAction(f.db, testEnqueuer, newGroupAgentCoordinator(f.db)).Execute(ctx, f.member, f.groupID))
	var applicationError *appservice.Error
	require.ErrorAs(t, report(memberToken, f.groupID, true), &applicationError, "left member report")
	require.Equal(t, http.StatusNotFound, applicationError.HTTPStatus(), "left member report")
	require.NoError(t, report(ownerToken, f.groupID, true))
	member.expectNoTyping()

	// 其他企业的会话与非法编号按会话不存在处理。
	require.ErrorAs(t, report(ownerToken, uuid.NewV7().String(), true), &applicationError, "unknown conversation")
	require.Equal(t, http.StatusNotFound, applicationError.HTTPStatus(), "unknown conversation")
	require.ErrorAs(t, report(ownerToken, "not-a-uuid", true), &applicationError, "invalid conversation")
	require.Equal(t, http.StatusNotFound, applicationError.HTTPStatus(), "invalid conversation")
}

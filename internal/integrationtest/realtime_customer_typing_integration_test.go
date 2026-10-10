//go:build server

package integrationtest

import (
	"context"
	"testing"
	"uuid"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// loadVisitorChatSubjectID 读取客户会话所属访客的聊天主体编号。
func loadVisitorChatSubjectID(t *testing.T, db *bun.DB, workspaceID, conversationID string) string {
	t.Helper()
	var value string
	err := db.NewSelect().TableExpr("channel_conversations AS cc").
		ColumnExpr("cs.id").
		Join("JOIN channel_identities AS ci ON ci.workspace_id = cc.workspace_id AND ci.id = cc.channel_identity_id").
		Join("JOIN chat_subjects AS cs ON cs.workspace_id = cc.workspace_id AND cs.kind = ? AND cs.source_id = ci.contact_id", domain.ChatSubjectKindContact).
		Where("cc.workspace_id = ? AND cc.conversation_id = ?", workspaceID, conversationID).
		Scan(context.Background(), &value)
	require.NoError(t, err)
	return value
}

// TestCustomerConversationTyping 验证客服对客输入只发给本线程访客、访客输入发给企业客服共享受众，客服无对客回复资格或线程不属于该访客时不发布。
func TestCustomerConversationTyping(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	workspaceID := f.owner.Workspace.ID
	feed := startRealtimeFeed(t, workspaceID)
	channelIdentityID := loadChannelIdentityID(t, f.db, f.conversationID)
	visitorSubjectID := loadVisitorChatSubjectID(t, f.db, workspaceID, f.conversationID)
	coordinator := newTestAgentRun(f.db, testEnqueuer, nil, testModelInvoker(f.db), nil, nil, servertest.DisabledMail{}, nil, nil)
	memberTyping := conversationaction.NewReportConversationTypingAction(f.db)
	visitorTyping := customerchataction.NewReportWebsiteVisitorTypingAction(f.db)

	// 访客输入送达企业客服共享受众，发送者为访客聊天主体。
	require.NoError(t, visitorTyping.Execute(ctx, f.channelID, websiteVisitorExternalID, f.conversationID, true))
	// 客服在未分配周期中对客输入送达本线程访客。
	require.NoError(t, memberTyping.Execute(ctx, f.owner, f.conversationID, true))
	require.NoError(t, memberTyping.Execute(ctx, f.owner, f.conversationID, false))
	feed.expectTyping(t,
		feed.customerInboxTyping(f.conversationID, visitorSubjectID, true),
		feed.visitorTyping(channelIdentityID, f.conversationID, true),
		feed.visitorTyping(channelIdentityID, f.conversationID, false),
	)

	// 其他访客身份与不存在的线程按会话不存在处理，不发布任何通知。
	require.ErrorIs(t, visitorTyping.Execute(ctx, f.channelID, "web-session:ffffffffffffffffffffffffffffffff", f.conversationID, true), conversationaction.ErrConversationNotFound, "其他访客上报")
	require.ErrorIs(t, visitorTyping.Execute(ctx, f.channelID, websiteVisitorExternalID, uuid.NewV7().String(), true), conversationaction.ErrConversationNotFound, "未知线程上报")

	// 周期由他人负责时客服不再向访客上报。
	_, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, testEnqueuer).Execute(ctx, f.member, f.conversationID)
	require.NoError(t, err)
	require.ErrorIs(t, memberTyping.Execute(ctx, f.owner, f.conversationID, true), conversationaction.ErrConversationNotFound, "他人负责时上报")
	require.NoError(t, memberTyping.Execute(ctx, f.member, f.conversationID, true))

	// 周期关闭后客服不再上报；访客仍可发起新一轮沟通，输入状态照常送达。
	_, err = servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, testEnqueuer).Execute(ctx, f.member, f.conversationID)
	require.NoError(t, err)
	require.ErrorIs(t, memberTyping.Execute(ctx, f.member, f.conversationID, true), conversationaction.ErrConversationNotFound, "周期关闭后上报")
	require.NoError(t, visitorTyping.Execute(ctx, f.channelID, websiteVisitorExternalID, f.conversationID, true))
	feed.expectTyping(t,
		feed.visitorTyping(channelIdentityID, f.conversationID, true),
		feed.customerInboxTyping(f.conversationID, visitorSubjectID, true),
	)
}

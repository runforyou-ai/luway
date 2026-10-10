//go:build server

package integrationtest

import (
	"context"
	"slices"
	"testing"

	"uuid"

	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/domain"
	servertest "github.com/runforyou-ai/luway/internal/servertest"
	"github.com/stretchr/testify/require"
)

// TestSearchCustomerHistory 验证客户历史检索只返回同一客户已关闭周期中的对客消息，并带出周期信息与发送方。
func TestSearchCustomerHistory(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	tasks := servertest.NewTasks()
	coordinator := newGroupAgentCoordinator(f.db)
	closeSession := servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, tasks)
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, testEnqueuer)
	// closeWith 在客户会话中依次发送客户消息、对客回复与内部备注后关闭当前周期，首次回复隐式领取周期。
	closeWith := func(conversationID, externalID, customerBody, replyBody, noteBody string) {
		t.Helper()
		_, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
			ChannelID: f.channelID, ExternalID: externalID, ConversationID: &conversationID, ClientMessageID: uuid.NewV7().String(), Body: customerBody,
		})
		require.NoError(t, err)
		for _, message := range []struct {
			body       string
			visibility domain.MessageVisibility
		}{{replyBody, domain.MessageVisibilityShared}, {noteBody, domain.MessageVisibilityInternal}} {
			_, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
				ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: message.body, Visibility: message.visibility,
			})
			require.NoError(t, err)
		}
		_, err = closeSession.Execute(ctx, f.owner, conversationID)
		require.NoError(t, err)
	}
	const externalID = "web-session:0123456789abcdef0123456789abcdef"
	first := loadSummarySession(t, f.db, f.conversationID)
	// 当前周期进行中时不在检索范围内。
	ongoing, err := servicesummary.SearchHistory(ctx, f.db, f.owner.Workspace.ID, first.ID, nil, "客户首条消息")
	require.NoError(t, err)
	require.Empty(t, ongoing.Sessions)
	require.NotEmpty(t, ongoing.Message)
	// 带说明的附件同时返回说明与文件名。
	fileID := uploadedAttachment(t, f.db, f.owner, "REVIEW731退款回执.pdf", "application/pdf")
	_, err = servicesessionaction.NewSendServiceAttachmentMessageAction(f.db, testEnqueuer).Execute(ctx, f.owner, servicesessionaction.ServiceAttachmentMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), FileID: fileID, Body: "请查收",
	})
	require.NoError(t, err)
	closeWith(f.conversationID, externalID, "订单 A1001 什么时候发货", "订单 A1001 已经发货", "A1001 内部备注")

	// 另一位客户的已关闭周期含相同订单号，不进入检索结果。
	other, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: "web-session:fedcba9876543210fedcba9876543210", ClientMessageID: uuid.NewV7().String(), Body: "另一位客户",
	})
	require.NoError(t, err)
	closeWith(other.Conversation.ID, "web-session:fedcba9876543210fedcba9876543210", "A1001 也是我的订单", "已记录 A1001", "A1001 另一备注")

	_, err = f.visitorMessage(ctx, "还在吗")
	require.NoError(t, err)
	current := loadSummarySession(t, f.db, f.conversationID)
	require.NotEqual(t, first.ID, current.ID, "客户新消息应开启新周期")
	result, err := servicesummary.SearchHistory(ctx, f.db, f.owner.Workspace.ID, current.ID, nil, "A1001 发货")
	require.NoError(t, err)
	require.Len(t, result.Sessions, 1)
	require.False(t, result.Sessions[0].ClosedAt.IsZero())
	var senders, bodies []string
	for _, message := range result.Sessions[0].Messages {
		senders, bodies = append(senders, message.Sender), append(bodies, message.Body)
	}
	require.Equal(t, []string{"客户首条消息", "请查收", "订单 A1001 什么时候发货", "订单 A1001 已经发货"}, bodies)
	require.Equal(t, []string{"customer", "member", "customer", "member"}, senders)
	// 只命中回复时，前文按对客消息计数带出，领取等系统事件和内部备注不占条数。
	single, err := servicesummary.SearchHistory(ctx, f.db, f.owner.Workspace.ID, current.ID, nil, "已经发货")
	require.NoError(t, err)
	require.Len(t, single.Sessions, 1)
	require.Len(t, single.Sessions[0].Messages, 3)
	require.Equal(t, "订单 A1001 什么时候发货", single.Sessions[0].Messages[1].Body)
	receipt, err := servicesummary.SearchHistory(ctx, f.db, f.owner.Workspace.ID, current.ID, nil, "REVIEW731")
	require.NoError(t, err)
	require.Len(t, receipt.Sessions, 1)
	require.True(t, slices.ContainsFunc(receipt.Sessions[0].Messages, func(message agentcontract.CustomerHistoryMessage) bool {
		return message.Body == "请查收" && message.Attachment == "REVIEW731退款回执.pdf"
	}), "附件命中的检索结果 = %+v", receipt)
	missed, err := servicesummary.SearchHistory(ctx, f.db, f.owner.Workspace.ID, current.ID, nil, "发票")
	require.NoError(t, err)
	require.Empty(t, missed.Sessions)
	require.NotEmpty(t, missed.Message)
	// 限定关闭时间上限时，之后关闭的周期不在检索范围内。
	closedBefore := result.Sessions[0].ClosedAt
	limited, err := servicesummary.SearchHistory(ctx, f.db, f.owner.Workspace.ID, current.ID, &closedBefore, "A1001 发货")
	require.NoError(t, err)
	require.Empty(t, limited.Sessions)
}

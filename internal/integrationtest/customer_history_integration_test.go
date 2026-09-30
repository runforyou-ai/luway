//go:build server

package integrationtest

import (
	"context"
	"slices"
	"testing"

	customerchataction "github.com/runforyou-ai/cervi/internal/actions/customerchat"
	servicesessionaction "github.com/runforyou-ai/cervi/internal/actions/servicesession"
	"github.com/runforyou-ai/cervi/internal/actions/servicesummary"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/integration/agentruntime"
	"uuid"
)

// TestSearchCustomerHistory 验证客户历史检索只返回同一客户已关闭周期中的对客消息，并带出周期信息与发送方。
func TestSearchCustomerHistory(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	tasks := newTestTasks(f.db)
	coordinator := newGroupAgentCoordinator(f.db)
	closeSession := servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, tasks)
	send := servicesessionaction.NewSendServiceTextMessageAction(f.db, nil)
	// closeWith 在客户会话中依次发送客户消息、对客回复与内部备注后关闭当前周期，首次回复隐式领取周期。
	closeWith := func(conversationID, externalID, customerBody, replyBody, noteBody string) {
		t.Helper()
		if _, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
			ChannelID: f.channelID, ExternalID: externalID, ConversationID: &conversationID, ClientMessageID: uuid.NewV7().String(), Body: customerBody,
		}); err != nil {
			t.Fatal(err)
		}
		for _, message := range []struct {
			body       string
			visibility domain.MessageVisibility
		}{{replyBody, domain.MessageVisibilityShared}, {noteBody, domain.MessageVisibilityInternal}} {
			if _, err := send.Execute(ctx, f.owner, servicesessionaction.ServiceTextMessageInput{
				ConversationID: conversationID, ClientMessageID: uuid.NewV7().String(), Body: message.body, Visibility: message.visibility,
			}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := closeSession.Execute(ctx, f.owner, conversationID); err != nil {
			t.Fatal(err)
		}
	}
	const externalID = "web-session:0123456789abcdef0123456789abcdef"
	first := loadSummarySession(t, f.db, f.conversationID)
	// 当前周期进行中时不在检索范围内。
	if result, err := servicesummary.SearchHistory(ctx, f.db, f.owner.Organization.ID, first.ID, nil, "客户首条消息"); err != nil || len(result.Sessions) != 0 || result.Message == "" {
		t.Fatalf("进行中周期的检索结果 = %+v, error = %v", result, err)
	}
	// 带说明的附件同时返回说明与文件名。
	fileID := uploadedAttachment(t, f.db, f.owner, "REVIEW731退款回执.pdf", "application/pdf")
	if _, err := servicesessionaction.NewSendServiceAttachmentMessageAction(f.db, nil).Execute(ctx, f.owner, servicesessionaction.ServiceAttachmentMessageInput{
		ConversationID: f.conversationID, ClientMessageID: uuid.NewV7().String(), FileID: fileID, Body: "请查收",
	}); err != nil {
		t.Fatal(err)
	}
	closeWith(f.conversationID, externalID, "订单 A1001 什么时候发货", "订单 A1001 已经发货", "A1001 内部备注")

	// 另一位客户的已关闭周期含相同订单号，不进入检索结果。
	other, err := f.receive.Execute(ctx, customerchataction.WebsiteCustomerTextMessageInput{
		ChannelID: f.channelID, ExternalID: "web-session:fedcba9876543210fedcba9876543210", ClientMessageID: uuid.NewV7().String(), Body: "另一位客户",
	})
	if err != nil {
		t.Fatal(err)
	}
	closeWith(other.Conversation.ID, "web-session:fedcba9876543210fedcba9876543210", "A1001 也是我的订单", "已记录 A1001", "A1001 另一备注")

	if _, err := f.visitorMessage(ctx, "还在吗"); err != nil {
		t.Fatal(err)
	}
	current := loadSummarySession(t, f.db, f.conversationID)
	if current.ID == first.ID {
		t.Fatal("客户新消息应开启新周期")
	}
	result, err := servicesummary.SearchHistory(ctx, f.db, f.owner.Organization.ID, current.ID, nil, "A1001 发货")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 1 || result.Sessions[0].ClosedAt.IsZero() {
		t.Fatalf("客户历史检索结果 = %+v", result)
	}
	var senders, bodies []string
	for _, message := range result.Sessions[0].Messages {
		senders, bodies = append(senders, message.Sender), append(bodies, message.Body)
	}
	if len(bodies) != 4 || bodies[0] != "客户首条消息" || bodies[1] != "请查收" || bodies[2] != "订单 A1001 什么时候发货" || bodies[3] != "订单 A1001 已经发货" ||
		senders[0] != "customer" || senders[1] != "member" || senders[2] != "customer" || senders[3] != "member" {
		t.Fatalf("命中周期的消息 = %v，发送方 = %v", bodies, senders)
	}
	// 只命中回复时，前文按对客消息计数带出，领取等系统事件和内部备注不占条数。
	single, err := servicesummary.SearchHistory(ctx, f.db, f.owner.Organization.ID, current.ID, nil, "已经发货")
	if err != nil {
		t.Fatal(err)
	}
	if len(single.Sessions) != 1 || len(single.Sessions[0].Messages) != 3 || single.Sessions[0].Messages[1].Body != "订单 A1001 什么时候发货" {
		t.Fatalf("单条命中的检索结果 = %+v", single)
	}
	receipt, err := servicesummary.SearchHistory(ctx, f.db, f.owner.Organization.ID, current.ID, nil, "REVIEW731")
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Sessions) != 1 || !slices.ContainsFunc(receipt.Sessions[0].Messages, func(message agentruntime.CustomerHistoryMessage) bool {
		return message.Body == "请查收" && message.Attachment == "REVIEW731退款回执.pdf"
	}) {
		t.Fatalf("附件命中的检索结果 = %+v", receipt)
	}
	if result, err := servicesummary.SearchHistory(ctx, f.db, f.owner.Organization.ID, current.ID, nil, "发票"); err != nil || len(result.Sessions) != 0 || result.Message == "" {
		t.Fatalf("无命中的检索结果 = %+v, error = %v", result, err)
	}
	// 限定关闭时间上限时，之后关闭的周期不在检索范围内。
	closedBefore := result.Sessions[0].ClosedAt
	if limited, err := servicesummary.SearchHistory(ctx, f.db, f.owner.Organization.ID, current.ID, &closedBefore, "A1001 发货"); err != nil || len(limited.Sessions) != 0 {
		t.Fatalf("限定关闭时间的检索结果 = %+v, error = %v", limited, err)
	}
}

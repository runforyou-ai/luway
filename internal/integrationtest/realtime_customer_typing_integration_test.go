//go:build server

package integrationtest

import (
	"context"
	"errors"
	"testing"
	"uuid"

	agentrunaction "github.com/runforyou-ai/luway/internal/actions/agentrun"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	customerchataction "github.com/runforyou-ai/luway/internal/actions/customerchat"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// loadVisitorChatSubjectID 读取客户会话所属访客的聊天主体编号。
func loadVisitorChatSubjectID(t *testing.T, db *bun.DB, organizationID, conversationID string) string {
	t.Helper()
	var value string
	err := db.NewSelect().TableExpr("channel_conversations AS cc").
		ColumnExpr("cs.id").
		Join("JOIN contact_channel_identities AS cci ON cci.organization_id = cc.organization_id AND cci.id = cc.contact_channel_identity_id").
		Join("JOIN chat_subjects AS cs ON cs.organization_id = cc.organization_id AND cs.kind = ? AND cs.source_id = cci.contact_id", domain.ChatSubjectKindContact).
		Where("cc.organization_id = ? AND cc.conversation_id = ?", organizationID, conversationID).
		Scan(context.Background(), &value)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// TestCustomerConversationTyping 验证客服对客输入只发给本线程访客、访客输入发给企业客服共享受众，客服无对客回复资格或线程不属于该访客时不发布。
func TestCustomerConversationTyping(t *testing.T) {
	t.Parallel()
	f := newCustomerReadFixture(t)
	ctx := context.Background()
	organizationID := f.owner.Organization.ID
	feed := startRealtimeFeed(t, organizationID)
	channelIdentityID := loadChannelIdentityID(t, f.db, f.conversationID)
	visitorSubjectID := loadVisitorChatSubjectID(t, f.db, organizationID, f.conversationID)
	coordinator := agentrunaction.NewExecuteAction(f.db, newTestTasks(f.db), nil, testModelInvoker(f.db), nil, nil, nil)
	memberTyping := conversationaction.NewReportConversationTypingAction(f.db)
	visitorTyping := customerchataction.NewReportWebsiteVisitorTypingAction(f.db)

	// 访客输入送达企业客服共享受众，发送者为访客聊天主体。
	if err := visitorTyping.Execute(ctx, f.channelID, websiteVisitorExternalID, f.conversationID, true); err != nil {
		t.Fatal(err)
	}
	// 客服在未分配周期中对客输入送达本线程访客。
	if err := memberTyping.Execute(ctx, f.owner, f.conversationID, true); err != nil {
		t.Fatal(err)
	}
	if err := memberTyping.Execute(ctx, f.owner, f.conversationID, false); err != nil {
		t.Fatal(err)
	}
	feed.expectTyping(t,
		feed.customerInboxTyping(f.conversationID, visitorSubjectID, true),
		feed.visitorTyping(channelIdentityID, f.conversationID, true),
		feed.visitorTyping(channelIdentityID, f.conversationID, false),
	)

	// 其他访客身份与不存在的线程按会话不存在处理，不发布任何通知。
	if err := visitorTyping.Execute(ctx, f.channelID, "web-session:ffffffffffffffffffffffffffffffff", f.conversationID, true); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("其他访客上报 err = %v", err)
	}
	if err := visitorTyping.Execute(ctx, f.channelID, websiteVisitorExternalID, uuid.NewV7().String(), true); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("未知线程上报 err = %v", err)
	}

	// 周期由他人负责时客服不再向访客上报。
	if _, err := servicesessionaction.NewClaimServiceSessionAction(f.db, coordinator, newTestTasks(f.db)).Execute(ctx, f.member, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if err := memberTyping.Execute(ctx, f.owner, f.conversationID, true); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("他人负责时上报 err = %v", err)
	}
	if err := memberTyping.Execute(ctx, f.member, f.conversationID, true); err != nil {
		t.Fatal(err)
	}

	// 周期关闭后客服不再上报；访客仍可发起新一轮沟通，输入状态照常送达。
	if _, err := servicesessionaction.NewCloseServiceSessionAction(f.db, coordinator, newTestTasks(f.db)).Execute(ctx, f.member, f.conversationID); err != nil {
		t.Fatal(err)
	}
	if err := memberTyping.Execute(ctx, f.member, f.conversationID, true); !errors.Is(err, conversationaction.ErrConversationNotFound) {
		t.Fatalf("周期关闭后上报 err = %v", err)
	}
	if err := visitorTyping.Execute(ctx, f.channelID, websiteVisitorExternalID, f.conversationID, true); err != nil {
		t.Fatal(err)
	}
	feed.expectTyping(t,
		feed.visitorTyping(channelIdentityID, f.conversationID, true),
		feed.customerInboxTyping(f.conversationID, visitorSubjectID, true),
	)
}

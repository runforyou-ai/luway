//go:build server

package integrationtest

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"
	"uuid"

	directchataction "github.com/runforyou-ai/luway/internal/actions/directchat"
	groupchataction "github.com/runforyou-ai/luway/internal/actions/groupchat"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime/protocol"
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
	organizationID := f.owner.Organization.ID
	h := startRealtimeGateway(t, f, testGatewayOptions(), nil)
	ownerToken := loginToken(t, f.db, organizationID, f.owner.Account.Email)
	memberToken := loginToken(t, f.db, organizationID, f.member.Account.Email)
	ownerOther, _ := h.connect(t, loginToken(t, f.db, organizationID, f.owner.Account.Email))
	member, _ := h.connect(t, memberToken)
	var ownerSubjectID string
	if err := f.db.NewSelect().Table("chat_subjects").Column("id").
		Where("organization_id = ? AND kind = ? AND source_id = ?", organizationID, domain.ChatSubjectKindOrganizationIdentity, f.owner.OrganizationIdentity.ID).
		Scan(ctx, &ownerSubjectID); err != nil {
		t.Fatal(err)
	}
	report := func(token, conversationID string, active bool) error {
		return h.backend.ReportConversationTyping(ctx, appservice.RequestMeta{Token: token, WorkspaceID: organizationID}, conversationID, appservice.ConversationTypingInput{Active: active})
	}

	// 群主输入送达群成员，群主其他设备不收到本人输入状态。
	if err := report(ownerToken, f.groupID, true); err != nil {
		t.Fatal(err)
	}
	if got, want := member.nextTyping(), (protocol.ConversationTyping{ConversationID: f.groupID, SenderSubjectID: ownerSubjectID, Active: true}); !reflect.DeepEqual(got, want) {
		t.Fatalf("typing = %#v, want %#v", got, want)
	}
	if err := report(ownerToken, f.groupID, false); err != nil {
		t.Fatal(err)
	}
	if got := member.nextTyping(); got.Active || got.SenderSubjectID != ownerSubjectID {
		t.Fatalf("stopped typing = %#v", got)
	}
	ownerOther.expectNoTyping()

	// 单聊输入送达对方。
	direct, err := directchataction.NewSendFirstDirectTextMessageAction(f.db).Execute(ctx, f.owner, directchataction.FirstDirectTextMessageInput{TargetIdentityID: f.member.OrganizationIdentity.ID, ClientMessageID: uuid.NewV7().String(), Body: "单聊"})
	if err != nil {
		t.Fatal(err)
	}
	if err := report(memberToken, direct.Conversation.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, want := ownerOther.nextTyping(), (protocol.ConversationTyping{ConversationID: direct.Conversation.ID, SenderSubjectID: f.subjectID, Active: true}); !reflect.DeepEqual(got, want) {
		t.Fatalf("direct typing = %#v, want %#v", got, want)
	}

	// 退出群聊后无法上报，也不再收到该群的输入状态。
	if err := groupchataction.NewLeaveGroupConversationAction(f.db, newGroupAgentCoordinator(f.db)).Execute(ctx, f.member, f.groupID); err != nil {
		t.Fatal(err)
	}
	var applicationError *appservice.Error
	if err := report(memberToken, f.groupID, true); !errors.As(err, &applicationError) || applicationError.HTTPStatus() != http.StatusNotFound {
		t.Fatalf("left member report err = %v", err)
	}
	if err := report(ownerToken, f.groupID, true); err != nil {
		t.Fatal(err)
	}
	member.expectNoTyping()

	// 其他企业的会话与非法编号按会话不存在处理。
	if err := report(ownerToken, uuid.NewV7().String(), true); !errors.As(err, &applicationError) || applicationError.HTTPStatus() != http.StatusNotFound {
		t.Fatalf("unknown conversation err = %v", err)
	}
	if err := report(ownerToken, "not-a-uuid", true); !errors.As(err, &applicationError) || applicationError.HTTPStatus() != http.StatusNotFound {
		t.Fatalf("invalid conversation err = %v", err)
	}
}

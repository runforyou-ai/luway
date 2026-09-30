//go:build server

package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	channelaction "github.com/runforyou-ai/cervi/internal/actions/channel"
	customerchataction "github.com/runforyou-ai/cervi/internal/actions/customerchat"
	"github.com/runforyou-ai/cervi/internal/integration/telegram"
)

// TestTelegramWebhook 验证公开回调认证、请求体限制和状态码契约。
func TestTelegramWebhook(t *testing.T) {
	tests := []struct {
		name          string
		secret        string
		body          string
		preflightErr  error
		executeErr    error
		status        int
		executeCalled bool
		myChatMember  bool
		message       *telegram.InboundMessage
	}{
		{name: "not found before body", secret: "secret", body: `{`, preflightErr: channelaction.ErrNotFound, status: http.StatusNotFound},
		{name: "unauthorized before body", secret: "wrong", body: `{`, preflightErr: customerchataction.ErrTelegramWebhookUnauthorized, status: http.StatusUnauthorized},
		{name: "malformed body", secret: "secret", body: `{`, status: http.StatusBadRequest},
		{name: "missing update id", secret: "secret", body: `{"my_chat_member":{}}`, status: http.StatusBadRequest},
		{name: "trailing JSON", secret: "secret", body: `{"update_id":1,"my_chat_member":{}} {}`, status: http.StatusBadRequest},
		{name: "invalid member update", secret: "secret", body: `{"update_id":1,"my_chat_member":true}`, status: http.StatusBadRequest},
		{name: "multiple update types", secret: "secret", body: `{"update_id":1,"my_chat_member":{},"message":{}}`, status: http.StatusBadRequest},
		{name: "unknown update", secret: "secret", body: `{"update_id":1,"callback_query":{}}`, status: http.StatusNoContent, executeCalled: true},
		{name: "ignored group message", secret: "secret", body: `{"update_id":1,"message":{"message_id":2,"date":1725000000,"chat":{"id":-1,"type":"group"}}}`, status: http.StatusNoContent, executeCalled: true},
		{name: "ignored private non-text", secret: "secret", body: `{"update_id":1,"message":{"message_id":2,"date":1725000000,"chat":{"id":3,"type":"private"},"from":{"id":3,"first_name":"Demo"}}}`, status: http.StatusNoContent, executeCalled: true},
		{name: "ignored cursor overflow date", secret: "secret", body: `{"update_id":1,"message":{"message_id":2,"date":9223372037,"text":"hello","chat":{"id":3,"type":"private"},"from":{"id":3,"first_name":"Demo"}}}`, status: http.StatusNoContent, executeCalled: true},
		{name: "temporary execute failure", secret: "secret", body: `{"update_id":1,"callback_query":{}}`, executeErr: errors.New("database unavailable"), status: http.StatusServiceUnavailable, executeCalled: true},
		{
			name: "valid private text", secret: "secret",
			body:   `{"update_id":1,"message":{"message_id":2,"date":1725000000,"text":"  hello  ","chat":{"id":3,"type":"private"},"from":{"id":3,"first_name":"Demo","last_name":"User"}}}`,
			status: http.StatusNoContent, executeCalled: true,
			message: &telegram.InboundMessage{
				ChatID: 3, MessageID: 2, SenderID: 3, DisplayName: "Demo User",
				Body: "hello", OriginatedAt: time.Unix(1725000000, 0).UTC(),
			},
		},
		{name: "valid callback", secret: "secret", body: `{"update_id":1,"my_chat_member":{}}`, status: http.StatusNoContent, executeCalled: true, myChatMember: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receiver := &telegramWebhookReceiverStub{
				preflightErr: test.preflightErr,
				executeErr:   test.executeErr,
			}
			service := NewService(nil, WithTelegramWebhook(receiver))
			request := httptest.NewRequest(
				http.MethodPost,
				"/public/telegram-channels/channel-id/webhook",
				strings.NewReader(test.body),
			)
			request.Header.Set("X-Telegram-Bot-Api-Secret-Token", test.secret)
			response := httptest.NewRecorder()

			service.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			if receiver.executeCalled != test.executeCalled {
				t.Fatalf("execute called = %t, want %t", receiver.executeCalled, test.executeCalled)
			}
			if receiver.executeCalled && receiver.input.MyChatMember != test.myChatMember {
				t.Fatalf("my_chat_member = %t, want %t", receiver.input.MyChatMember, test.myChatMember)
			}
			if receiver.executeCalled && !telegramWebhookMessagesEqual(receiver.input.Message, test.message) {
				t.Fatalf("message = %#v, want %#v", receiver.input.Message, test.message)
			}
			if receiver.secret != test.secret {
				t.Fatalf("preflight secret = %q, want %q", receiver.secret, test.secret)
			}
			if response.Body.Len() != 0 {
				t.Fatalf("response body = %q, want empty", response.Body.String())
			}
		})
	}
}

// TestTelegramWebhookRejectsOversizedBody 验证超过 64 KiB 的回调体被拒绝。
func TestTelegramWebhookRejectsOversizedBody(t *testing.T) {
	receiver := &telegramWebhookReceiverStub{}
	service := NewService(nil, WithTelegramWebhook(receiver))
	body := `{"update_id":1,"my_chat_member":{"value":"` + strings.Repeat("x", telegramWebhookBodyLimit) + `"}}`
	request := httptest.NewRequest(http.MethodPost, "/public/telegram-channels/channel-id/webhook", strings.NewReader(body))
	request.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	response := httptest.NewRecorder()

	service.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if receiver.executeCalled {
		t.Fatal("oversized body reached action")
	}
}

type telegramWebhookReceiverStub struct {
	preflightErr  error
	executeErr    error
	secret        string
	input         customerchataction.TelegramWebhookInput
	executeCalled bool
}

// Preflight 记录测试请求头并返回预设错误。
func (s *telegramWebhookReceiverStub) Preflight(_ context.Context, _ string, secret string) error {
	s.secret = secret
	return s.preflightErr
}

// Execute 记录解析后的回调输入并返回预设错误。
func (s *telegramWebhookReceiverStub) Execute(_ context.Context, _ string, input customerchataction.TelegramWebhookInput) error {
	s.executeCalled = true
	s.input = input
	return s.executeErr
}

// telegramWebhookMessagesEqual 比较回调归一化消息。
func telegramWebhookMessagesEqual(left, right *telegram.InboundMessage) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.ChatID == right.ChatID && left.MessageID == right.MessageID && left.SenderID == right.SenderID &&
		left.DisplayName == right.DisplayName && left.Body == right.Body && left.OriginatedAt.Equal(right.OriginatedAt)
}

var _ TelegramWebhookReceiver = (*telegramWebhookReceiverStub)(nil)

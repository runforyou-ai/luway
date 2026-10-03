//go:build server

package direct

import (
	"errors"
	"testing"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	fileaction "github.com/runforyou-ai/luway/internal/actions/file"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
)

// TestConversationMessageCursorRejectsAnotherConversation 验证消息游标的会话归属校验。
func TestConversationMessageCursorRejectsAnotherConversation(t *testing.T) {
	point := conversationaction.MessageCursorPoint{
		MessageSeq: 9007199254740993,
		ID:         "0198ddf0-a234-7f01-8d99-e3e0af0f5f65",
	}
	cursor := encodeConversationMessageCursor("0198ddee-c056-7bc5-a1d9-586f878ee966", point)
	if _, valid := decodeConversationMessageCursor(cursor, "0198ddee-c056-7bc5-a1d9-586f878ee977"); valid {
		t.Fatal("expected cross-conversation cursor to be rejected")
	}
}

// TestServiceTextMessageErrorMapsConflicts 验证成员回复冲突保留稳定原因。
func TestServiceTextMessageErrorMapsConflicts(t *testing.T) {
	tests := []struct {
		reason  string
		message string
	}{
		{reason: conversationaction.ConflictReasonIdempotencyMismatch, message: "这条消息与之前的发送内容不一致。"},
		{reason: conversationaction.ConflictReasonServiceSessionOwned, message: "这条会话已由其他客服负责。"},
		{reason: conversationaction.ConflictReasonServiceSessionNotReplyable, message: "当前客服处理周期无法继续处理。"},
		{reason: conversationaction.ConflictReasonChannelOutboundUnsupported, message: "当前消息渠道暂不支持回复。"},
	}
	for _, test := range tests {
		err := serviceTextMessageError(appservice.RequestMeta{Locale: appservice.LocaleChineseSimplified}, &conversationaction.ConflictError{Reason: test.reason})
		var apiError *appservice.Error
		if !errors.As(err, &apiError) || apiError.Kind != appservice.ErrorKindConflict || apiError.Reason != test.reason || apiError.Message != test.message {
			t.Fatalf("error = %#v", err)
		}
	}
}

// TestMessageCursorPreservesSequence 验证消息游标无损传输大整数序号。
func TestMessageCursorPreservesSequence(t *testing.T) {
	const conversationID = "0198ddee-c056-7bc5-a1d9-586f878ee966"
	sequence := int64(9007199254740993)
	point := conversationaction.MessageCursorPoint{MessageSeq: sequence, ID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f65"}
	cursor := encodeConversationMessageCursor(conversationID, point)
	decoded, valid := decodeConversationMessageCursor(cursor, conversationID)
	if !valid || decoded.MessageSeq != sequence || decoded.ID != point.ID {
		t.Fatalf("decoded=%+v valid=%v", decoded, valid)
	}
	if got := messageSeqString(&sequence); got == nil || *got != "9007199254740993" {
		t.Fatalf("sequence=%v", got)
	}
}

// TestCustomerMessageErrorMapsFileNotFound 验证附件文件无效时对外返回文件未找到，而不是笼统的发送失败。
func TestCustomerMessageErrorMapsFileNotFound(t *testing.T) {
	err := serviceTextMessageError(appservice.RequestMeta{}, fileaction.ErrFileNotFound)
	applicationError, ok := errors.AsType[*appservice.Error](err)
	if !ok || applicationError.Kind != appservice.ErrorKindNotFound {
		t.Fatalf("mapped error = %#v", err)
	}
	expected, _ := i18n.Localize("", i18n.ErrorFileNotFound)
	if applicationError.Message != expected {
		t.Fatalf("message = %q", applicationError.Message)
	}
}

//go:build server

package directchat

import (
	"strings"
	"testing"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
)

// TestNormalizeDirectIdentityPair 验证双方发起得到同一规范化身份对。
func TestNormalizeDirectIdentityPair(t *testing.T) {
	firstIdentityID := "0198ddf0-a234-7f01-8d99-e3e0af0f5f65"
	secondIdentityID := "0198ddf0-a234-7f01-8d99-e3e0af0f5f66"
	forwardFirst, forwardSecond := normalizeDirectIdentityPair(firstIdentityID, secondIdentityID)
	reverseFirst, reverseSecond := normalizeDirectIdentityPair(secondIdentityID, firstIdentityID)
	if forwardFirst != firstIdentityID || forwardSecond != secondIdentityID || reverseFirst != firstIdentityID || reverseSecond != secondIdentityID {
		t.Fatalf("normalized pairs = (%q, %q) and (%q, %q)", forwardFirst, forwardSecond, reverseFirst, reverseSecond)
	}
}

// TestNormalizeInternalTextMessageInput 验证内部单聊文本消息输入归一化。
func TestNormalizeInternalTextMessageInput(t *testing.T) {
	input := InternalTextMessageInput{
		ConversationID:   "0198DDEE-C056-7BC5-A1D9-586F878EE966",
		ClientMessageID:  "0198DDF0-A234-7F01-8D99-E3E0AF0F5F65",
		Body:             "  你好，同事  ",
		ReplyToMessageID: "0198DDF0-A234-7F01-8D99-E3E0AF0F5F66",
	}
	normalized, fields := normalizeInternalMessageInput(input)
	if len(fields) != 0 || normalized.ReplyToMessageID != strings.ToLower(input.ReplyToMessageID) || normalized.Body != "你好，同事" || normalized.ConversationID != strings.ToLower(input.ConversationID) || normalized.ClientMessageID != strings.ToLower(input.ClientMessageID) {
		t.Fatalf("normalized = %#v, fields = %#v", normalized, fields)
	}
}

// TestNormalizeDirectReplyTarget 验证引用编号的选填规则和格式。
func TestNormalizeDirectReplyTarget(t *testing.T) {
	for _, reference := range []string{"", "invalid"} {
		_, fields := normalizeInternalMessageInput(InternalTextMessageInput{
			ConversationID:  "0198ddee-c056-7bc5-a1d9-586f878ee966",
			ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f65",
			Body:            "回复", ReplyToMessageID: reference,
		})
		if (fields["replyToMessageId"] == conversationaction.ValidationReplyToMessageIDInvalid) != (reference != "") {
			t.Fatalf("reference=%q fields=%v", reference, fields)
		}
	}
}

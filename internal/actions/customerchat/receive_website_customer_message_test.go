//go:build server

package customerchat

import (
	"testing"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/common/customeridentity"
)

// TestNormalizeWebsiteMessageInput 验证网站消息正文和身份输入规范化。
func TestNormalizeWebsiteMessageInput(t *testing.T) {
	input := WebsiteCustomerTextMessageInput{
		ChannelID:       "0198ddee-c056-7bc5-a1d9-586f878ee966",
		ExternalID:      "web-session:0123456789abcdef0123456789abcdef",
		ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f65",
		Body:            "  第一行\n第二行  ",
	}
	normalized, fields := normalizeWebsiteMessageInput(input)
	if len(fields) != 0 {
		t.Fatalf("validation fields = %#v", fields)
	}
	if normalized.Body != "第一行\n第二行" {
		t.Fatalf("normalized body = %q", normalized.Body)
	}
}

// TestNormalizeWebsiteReplyTarget 验证引用编号格式和新会话的引用限制。
func TestNormalizeWebsiteReplyTarget(t *testing.T) {
	conversationID := "0198ddee-c056-7bc5-a1d9-586f878ee966"
	for _, input := range []WebsiteCustomerTextMessageInput{
		{ReplyToMessageID: "invalid", ConversationID: &conversationID},
		{ReplyToMessageID: conversationID},
	} {
		_, fields := normalizeWebsiteMessageInput(input)
		if fields["replyToMessageId"] != conversationaction.ValidationReplyToMessageIDInvalid {
			t.Fatalf("fields=%+v", fields)
		}
	}
	input, fields := normalizeWebsiteMessageInput(WebsiteCustomerTextMessageInput{ReplyToMessageID: "0198DDEE-C056-7BC5-A1D9-586F878EE966", ConversationID: &conversationID})
	if _, invalid := fields["replyToMessageId"]; invalid || input.ReplyToMessageID != conversationID {
		t.Fatalf("input=%+v fields=%+v", input, fields)
	}
}

// TestNormalizeWebsiteAttachmentMessageInput 验证附件说明可以为空、超长说明被拒绝，且文件编号必须合法。
func TestNormalizeWebsiteAttachmentMessageInput(t *testing.T) {
	base := WebsiteCustomerAttachmentMessageInput{
		ChannelID:       "0198ddee-c056-7bc5-a1d9-586f878ee966",
		ExternalID:      "web-session:0123456789abcdef0123456789abcdef",
		ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f65",
		FileID:          "0198ddf0-a234-7f01-8d99-e3e0af0f5f66",
	}
	normalized, fields := normalizeWebsiteAttachmentMessageInput(base)
	if len(fields) != 0 || normalized.Body != "" {
		t.Fatalf("normalized=%+v fields=%#v", normalized, fields)
	}
	caption := make([]rune, 4001)
	for index := range caption {
		caption[index] = '鹿'
	}
	long := base
	long.Body = string(caption)
	if _, fields := normalizeWebsiteAttachmentMessageInput(long); fields["body"] != conversationaction.ValidationBodyTooLong {
		t.Fatalf("fields=%#v", fields)
	}
	invalid := base
	invalid.FileID = "not-a-uuid"
	if _, fields := normalizeWebsiteAttachmentMessageInput(invalid); fields["fileId"] != conversationaction.ValidationFileIDInvalid {
		t.Fatalf("fields=%#v", fields)
	}
	negative := base
	negative.ImageHeight = -1
	if _, fields := normalizeWebsiteAttachmentMessageInput(negative); fields["fileId"] != conversationaction.ValidationFileIDInvalid {
		t.Fatalf("fields=%#v", fields)
	}
}

// TestWebsiteCustomerExternalIDMatchesIdentity 验证签名身份与外部编号必须一致。
func TestWebsiteCustomerExternalIDMatchesIdentity(t *testing.T) {
	input := WebsiteCustomerTextMessageInput{
		ChannelID:       "0198ddee-c056-7bc5-a1d9-586f878ee966",
		ExternalID:      customeridentity.CustomerExternalID("user-42"),
		ClientMessageID: "0198ddf0-a234-7f01-8d99-e3e0af0f5f65",
		Body:            "你好",
		Customer:        &SignedCustomer{UserID: "user-43"},
	}
	if _, fields := normalizeWebsiteMessageInput(input); fields["visitorToken"] != ValidationExternalIDInvalid {
		t.Fatalf("fields=%#v", fields)
	}
}

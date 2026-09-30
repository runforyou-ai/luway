//go:build server

package direct

import (
	"context"
	"errors"
	"testing"

	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	"github.com/runforyou-ai/cervi/internal/appservice"
)

// TestWebsiteVisitorErrorUsesCustomerLocale 验证访客接口错误按对客语言本地化并记录文案语言。
func TestWebsiteVisitorErrorUsesCustomerLocale(t *testing.T) {
	meta := appservice.WebsiteVisitorMeta{Locale: appservice.CustomerLocaleHindiIndia}
	err := websiteVisitorError(context.Background(), meta, conversationaction.ErrChannelNotFound, "", "send_text_message")
	var visitorError *appservice.Error
	if !errors.As(err, &visitorError) {
		t.Fatalf("error = %T, want *Error", err)
	}
	if visitorError.Message != "यह चैट अभी उपलब्ध नहीं है।" || visitorError.Language() != "hi-IN" || visitorError.Kind != appservice.ErrorKindNotFound {
		t.Fatalf("visitor error = %+v language=%q", visitorError, visitorError.Language())
	}

	validation := &conversationaction.ValidationError{Fields: map[string]conversationaction.ValidationCode{"body": conversationaction.ValidationBodyTooLong}}
	err = websiteVisitorError(context.Background(), meta, validation, "", "send_text_message")
	if !errors.As(err, &visitorError) || visitorError.Message != visitorError.Fields["body"] || visitorError.Message == "" {
		t.Fatalf("validation error = %+v", visitorError)
	}
}

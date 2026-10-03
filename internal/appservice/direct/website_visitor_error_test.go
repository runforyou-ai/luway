//go:build server

package direct

import (
	"errors"
	"testing"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/appservice"
)

// TestWebsiteVisitorErrorUsesCustomerLocale 验证访客接口错误按对客语言本地化并记录文案语言。
func TestWebsiteVisitorErrorUsesCustomerLocale(t *testing.T) {
	meta := appservice.WebsiteVisitorMeta{Locale: appservice.CustomerLocaleHindiIndia}
	err := websiteVisitorError(meta, conversationaction.ErrChannelNotFound, "")
	var visitorError *appservice.Error
	if !errors.As(err, &visitorError) {
		t.Fatalf("error = %T, want *Error", err)
	}
	if visitorError.Message != "यह चैट अभी उपलब्ध नहीं है।" || visitorError.Language() != "hi-IN" || visitorError.Kind != appservice.ErrorKindNotFound {
		t.Fatalf("visitor error = %+v language=%q", visitorError, visitorError.Language())
	}

	validation := &conversationaction.ValidationError{Fields: map[string]conversationaction.ValidationCode{"body": conversationaction.ValidationBodyTooLong}}
	err = websiteVisitorError(meta, validation, "")
	if !errors.As(err, &visitorError) || visitorError.Message != visitorError.Fields["body"] || visitorError.Message == "" {
		t.Fatalf("validation error = %+v", visitorError)
	}
}

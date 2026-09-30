package webfetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// testUserAgent 是测试请求使用的 User-Agent。
const testUserAgent = "Mozilla/5.0 (compatible; WebFetchTest/1.0)"

// TestNormalize 验证地址校验与片段清除。
func TestNormalize(t *testing.T) {
	address, err := Normalize("  https://example.com/help?topic=refund#section  ")
	if err != nil || address != "https://example.com/help?topic=refund" {
		t.Fatalf("address=%q err=%v", address, err)
	}
	for _, invalid := range []string{"", "/help", "ftp://example.com/a", "file:///etc/hosts", "https://"} {
		if _, err := Normalize(invalid); err == nil {
			t.Fatalf("expected rejection for %q", invalid)
		}
	}
}

// TestFetch 验证内容类型判定、状态码失败、体积上限与不可达地址。
func TestFetch(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name         string
		contentType  string
		body         string
		encodeGBK    bool
		status       int
		wantType     string
		wantContains string
		wantCode     string
	}{
		{name: "html", contentType: "text/html; charset=utf-8", body: helpPage, status: 200, wantType: ContentTypeHTML, wantContains: "七个自然日"},
		{name: "html gbk", contentType: "text/html; charset=gbk", body: helpPage, encodeGBK: true, status: 200, wantType: ContentTypeHTML, wantContains: "七个自然日"},
		{name: "plain", contentType: "text/plain", body: "退款说明\n<tag> 不是标记", status: 200, wantType: ContentTypeText},
		{name: "pdf", contentType: "application/pdf", body: "%PDF", status: 200, wantCode: "url_content_unsupported"},
		{name: "missing type", contentType: "", body: "内容", status: 200, wantCode: "url_content_unsupported"},
		{name: "not found", contentType: "text/html", body: "", status: 404, wantCode: "url_unreachable"},
		{name: "too large", contentType: "text/html", body: strings.Repeat("a", maxResponseBytes+1), status: 200, wantCode: "url_content_too_large"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Header.Get("User-Agent") != testUserAgent {
					t.Errorf("user agent=%q", request.Header.Get("User-Agent"))
				}
				if test.contentType != "" {
					writer.Header().Set("Content-Type", test.contentType)
				} else {
					writer.Header()["Content-Type"] = nil
				}
				writer.WriteHeader(test.status)
				body := []byte(test.body)
				if test.encodeGBK {
					encoded, err := simplifiedchinese.GBK.NewEncoder().Bytes(body)
					if err != nil {
						t.Errorf("encode: %v", err)
					}
					body = encoded
				}
				_, _ = writer.Write(body)
			}))
			defer server.Close()
			page, err := NewClient(testUserAgent).Fetch(ctx, server.URL+"/help")
			if test.wantCode != "" {
				var failure *Error
				if !errors.As(err, &failure) || failure.Code != test.wantCode {
					t.Fatalf("err=%v want=%s", err, test.wantCode)
				}
				return
			}
			if err != nil || page.ContentType != test.wantType {
				t.Fatalf("page=%+v err=%v", page, err)
			}
			if test.wantContains != "" {
				if !strings.Contains(string(page.Body), test.wantContains) {
					t.Fatalf("body=%q want %q", page.Body, test.wantContains)
				}
				return
			}
			if string(page.Body) != test.body {
				t.Fatalf("body=%q", page.Body)
			}
		})
	}

	// 连接被拒绝的地址按不可达处理。
	var failure *Error
	if _, err := NewClient(testUserAgent).Fetch(ctx, "http://127.0.0.1:9/help"); !errors.As(err, &failure) || failure.Code != "url_unreachable" {
		t.Fatalf("err=%v", err)
	}
}

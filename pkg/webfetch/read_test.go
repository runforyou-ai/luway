package webfetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRead 验证网页正文转换为 Markdown 并带出标题，空壳页按空正文失败。
func TestRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/empty" {
			_, _ = w.Write([]byte(`<!DOCTYPE html><html><body><div id="app"></div></body></html>`))
			return
		}
		_, _ = w.Write([]byte(helpPage))
	}))
	defer server.Close()
	client := NewClient(testUserAgent)
	document, err := client.Read(context.Background(), server.URL+"/help#top")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if document.URL != server.URL+"/help" || document.Title != "退款政策" {
		t.Fatalf("document=%+v", document)
	}
	for _, keep := range []string{"七个自然日", "| 对公转账 | 五个工作日 |", "POST /v1/refunds"} {
		if !strings.Contains(document.Content, keep) {
			t.Fatalf("content missing %q: %s", keep, document.Content)
		}
	}
	if strings.Contains(document.Content, "<p>") || strings.Contains(document.Content, "版权所有") {
		t.Fatalf("content=%s", document.Content)
	}
	var failure *Error
	if _, err := client.Read(context.Background(), server.URL+"/empty"); !errors.As(err, &failure) || failure.Code != "content_empty" {
		t.Fatalf("err=%v", err)
	}
}

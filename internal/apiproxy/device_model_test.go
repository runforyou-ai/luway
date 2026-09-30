//go:build !server

package apiproxy

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/runforyou-ai/cervi/internal/appservice"
)

// recordingTransport 记录发出的请求并返回空响应。
type recordingTransport struct {
	requests []*http.Request
}

// RoundTrip 记录请求并返回 200 空响应。
func (t *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.requests = append(t.requests, request)
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
}

// TestDeviceModelTransportStaysOnProxy 验证传输层只向模型代理入口发出请求并替换认证请求头，入口之外的地址不附加登录令牌。
func TestDeviceModelTransportStaysOnProxy(t *testing.T) {
	base := &recordingTransport{}
	endpoint, err := url.Parse("https://app.example.com/api/agent-runs/run-1/model")
	if err != nil {
		t.Fatal(err)
	}
	transport := &deviceModelTransport{base: base, endpoint: endpoint, token: "login-token", deviceID: "device-1"}

	request, _ := http.NewRequest(http.MethodPost, "https://app.example.com/api/agent-runs/run-1/model/v1/messages", nil)
	request.Header.Set("X-Api-Key", "placeholder")
	if _, err := transport.RoundTrip(request); err != nil {
		t.Fatal(err)
	}
	sent := base.requests[0]
	if sent.Header.Get("Authorization") != "Bearer login-token" || sent.Header.Get(appservice.DeviceHeader) != "device-1" ||
		sent.Header.Get("X-Api-Key") != "" || request.Header.Get("X-Api-Key") != "placeholder" {
		t.Fatalf("请求头 = %v，原请求头 = %v", sent.Header, request.Header)
	}

	for _, target := range []string{
		"https://api.anthropic.com/v1/messages",
		"https://app.example.com/api/agent-runs/run-1/v1/messages",
		"https://app.example.com/api/agent-runs/run-1/modelx/chat/completions",
		"http://app.example.com/api/agent-runs/run-1/model/chat/completions",
	} {
		outside, _ := http.NewRequest(http.MethodPost, target, nil)
		if _, err := transport.RoundTrip(outside); err == nil {
			t.Fatalf("%s 未被拒绝", target)
		}
	}
	if len(base.requests) != 1 {
		t.Fatalf("发出的请求数 = %d", len(base.requests))
	}
}

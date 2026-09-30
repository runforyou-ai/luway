//go:build server

package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/appservice/direct"
	"github.com/runforyou-ai/cervi/internal/i18n"
)

// stubDeviceModelAuthorizer 按预设返回上游模型服务或错误，并记录收到的认证信息。
type stubDeviceModelAuthorizer struct {
	upstream direct.DeviceModelUpstream
	err      error
	meta     appservice.RequestMeta
	runID    string
}

// AuthorizeDeviceModelRequest 记录认证信息并返回预设结果。
func (a *stubDeviceModelAuthorizer) AuthorizeDeviceModelRequest(_ context.Context, meta appservice.RequestMeta, runID string) (direct.DeviceModelUpstream, error) {
	a.meta, a.runID = meta, runID
	return a.upstream, a.err
}

// upstreamRecord 是上游模型服务收到的请求。
type upstreamRecord struct {
	path, query, authorization, apiKey, googleKey, device, body string
}

// newModelUpstream 启动记录请求并以流式分片应答的上游模型服务。
func newModelUpstream(t *testing.T, record *upstreamRecord) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		*record = upstreamRecord{
			path: request.URL.Path, query: request.URL.RawQuery, authorization: request.Header.Get("Authorization"),
			apiKey: request.Header.Get("X-Api-Key"), googleKey: request.Header.Get("X-Goog-Api-Key"),
			device: request.Header.Get(appservice.DeviceHeader), body: string(body),
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: first\n\ndata: second\n\n")
	}))
	t.Cleanup(server.Close)
	return server
}

// serveDeviceModel 以设备身份向模型代理发出一次请求。
func serveDeviceModel(authorizer DeviceModelAuthorizer, path, body string) *httptest.ResponseRecorder {
	service := NewService(nil, WithDeviceModelProxy(authorizer))
	request := httptest.NewRequest(http.MethodPost, "/agent-runs/run-1/model"+path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer login-token")
	request.Header.Set("X-Api-Key", "device-placeholder")
	request.Header.Set(appservice.DeviceHeader, "device-1")
	recorder := httptest.NewRecorder()
	service.ServeHTTP(recorder, request)
	return recorder
}

// TestDeviceModelProxyReplacesCredentials 验证代理以设备身份授权，换成供应商凭据转发并透传流式响应。
func TestDeviceModelProxyReplacesCredentials(t *testing.T) {
	record := upstreamRecord{}
	upstream := newModelUpstream(t, &record)
	authorizer := &stubDeviceModelAuthorizer{upstream: direct.DeviceModelUpstream{
		Brand: "openai", BaseURL: upstream.URL + "/v1", APIKey: "provider-key", Identifier: "gpt-test",
	}}

	recorder := serveDeviceModel(authorizer, "/chat/completions?key=leak", `{"model":"gpt-test","stream":true}`)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "data: first\n\ndata: second\n\n" {
		t.Fatalf("响应 = %d %q", recorder.Code, recorder.Body.String())
	}
	if authorizer.meta.Token != "login-token" || authorizer.meta.DeviceID != "device-1" || authorizer.runID != "run-1" {
		t.Fatalf("授权信息 = %#v %q", authorizer.meta, authorizer.runID)
	}
	if record.path != "/v1/chat/completions" || record.query != "" || record.authorization != "Bearer provider-key" ||
		record.apiKey != "" || record.device != "" || record.body != `{"model":"gpt-test","stream":true}` {
		t.Fatalf("上游请求 = %#v", record)
	}
}

// TestDeviceModelProxyBrandRules 验证代理按品牌去掉入口路径前缀并写入对应的凭据请求头。
func TestDeviceModelProxyBrandRules(t *testing.T) {
	record := upstreamRecord{}
	upstream := newModelUpstream(t, &record)

	alibaba := &stubDeviceModelAuthorizer{upstream: direct.DeviceModelUpstream{
		Brand: "alibaba", BaseURL: upstream.URL + "/compatible-mode/v1", APIKey: "qwen-key", Identifier: "qwen-test",
	}}
	if recorder := serveDeviceModel(alibaba, "/compatible-mode/v1/chat/completions", `{"model":"qwen-test"}`); recorder.Code != http.StatusOK {
		t.Fatalf("阿里云响应 = %d", recorder.Code)
	}
	if record.path != "/compatible-mode/v1/chat/completions" || record.authorization != "Bearer qwen-key" {
		t.Fatalf("阿里云上游请求 = %#v", record)
	}

	anthropic := &stubDeviceModelAuthorizer{upstream: direct.DeviceModelUpstream{
		Brand: "anthropic", BaseURL: upstream.URL, APIKey: "claude-key", Identifier: "claude-test",
	}}
	if recorder := serveDeviceModel(anthropic, "/v1/messages", `{"model":"claude-test"}`); recorder.Code != http.StatusOK {
		t.Fatalf("Anthropic 响应 = %d", recorder.Code)
	}
	if record.path != "/v1/messages" || record.apiKey != "claude-key" || record.authorization != "" {
		t.Fatalf("Anthropic 上游请求 = %#v", record)
	}

	volcengine := &stubDeviceModelAuthorizer{upstream: direct.DeviceModelUpstream{
		Brand: "volcengine", BaseURL: upstream.URL + "/api/v3", APIKey: "ark-key", Identifier: "doubao-test",
	}}
	if recorder := serveDeviceModel(volcengine, "/responses", `{"model":"doubao-test"}`); recorder.Code != http.StatusOK {
		t.Fatalf("火山引擎响应 = %d", recorder.Code)
	}
	if record.path != "/api/v3/responses" || record.authorization != "Bearer ark-key" {
		t.Fatalf("火山引擎上游请求 = %#v", record)
	}

	// Google 模型标识带或不带资源前缀时都按 SDK 生成的完整资源名放行。
	for identifier, path := range map[string]string{
		"gemini-test":         "/v1beta/models/gemini-test:streamGenerateContent",
		"models/gemini-test":  "/v1beta/models/gemini-test:generateContent",
		"tunedModels/my-tune": "/v1beta/tunedModels/my-tune:generateContent",
	} {
		google := &stubDeviceModelAuthorizer{upstream: direct.DeviceModelUpstream{
			Brand: "google", BaseURL: upstream.URL, APIKey: "gemini-key", Identifier: identifier,
		}}
		if recorder := serveDeviceModel(google, path+"?alt=sse", `{}`); recorder.Code != http.StatusOK {
			t.Fatalf("Google %s 响应 = %d", identifier, recorder.Code)
		}
		if record.path != path || record.query != "alt=sse" || record.googleKey != "gemini-key" || record.authorization != "" {
			t.Fatalf("Google %s 上游请求 = %#v", identifier, record)
		}
	}
}

// TestDeviceModelProxyRejects 验证非对话接口、路径穿越、模型与配置版本不一致、入口前缀不符或授权失败时不转发。
func TestDeviceModelProxyRejects(t *testing.T) {
	record := upstreamRecord{}
	upstream := newModelUpstream(t, &record)
	openAI := direct.DeviceModelUpstream{Brand: "openai", BaseURL: upstream.URL, APIKey: "provider-key", Identifier: "gpt-test"}
	for name, test := range map[string]struct {
		authorizer *stubDeviceModelAuthorizer
		path, body string
		status     int
	}{
		"模型不一致": {&stubDeviceModelAuthorizer{upstream: openAI}, "/chat/completions", `{"model":"gpt-other"}`, http.StatusBadRequest},
		"缺少模型":  {&stubDeviceModelAuthorizer{upstream: openAI}, "/chat/completions", `{}`, http.StatusBadRequest},
		"Google 模型不一致": {&stubDeviceModelAuthorizer{upstream: direct.DeviceModelUpstream{
			Brand: "google", BaseURL: upstream.URL, Identifier: "gemini-test",
		}}, "/v1beta/models/gemini-other:generateContent", `{}`, http.StatusBadRequest},
		"Google 路径穿越": {&stubDeviceModelAuthorizer{upstream: direct.DeviceModelUpstream{
			Brand: "google", BaseURL: upstream.URL, Identifier: "gemini-test",
		}}, "/v1beta/models/gemini-test:generateContent/../../models/gemini-other:generateContent", `{}`, http.StatusBadRequest},
		"Google 非对话接口": {&stubDeviceModelAuthorizer{upstream: direct.DeviceModelUpstream{
			Brand: "google", BaseURL: upstream.URL, Identifier: "gemini-test",
		}}, "/v1beta/models/gemini-test:embedContent", `{}`, http.StatusBadRequest},
		"非对话接口":   {&stubDeviceModelAuthorizer{upstream: openAI}, "/embeddings", `{"model":"gpt-test"}`, http.StatusBadRequest},
		"编码的路径穿越": {&stubDeviceModelAuthorizer{upstream: openAI}, "/chat/completions/%2e%2e/files", `{"model":"gpt-test"}`, http.StatusBadRequest},
		"Anthropic 非消息接口": {&stubDeviceModelAuthorizer{upstream: direct.DeviceModelUpstream{
			Brand: "anthropic", BaseURL: upstream.URL, Identifier: "claude-test",
		}}, "/v1/files", `{"model":"claude-test"}`, http.StatusBadRequest},
		"入口前缀不符": {&stubDeviceModelAuthorizer{upstream: direct.DeviceModelUpstream{
			Brand: "alibaba", BaseURL: upstream.URL + "/compatible-mode/v1", Identifier: "qwen-test",
		}}, "/chat/completions", `{"model":"qwen-test"}`, http.StatusBadRequest},
		"租约失效": {&stubDeviceModelAuthorizer{err: appservice.ConflictError(appservice.RequestMeta{}, i18n.ErrorDeviceRunLeaseLost, "lease_lost")},
			"/chat/completions", `{"model":"gpt-test"}`, http.StatusConflict},
	} {
		record = upstreamRecord{}
		if recorder := serveDeviceModel(test.authorizer, test.path, test.body); recorder.Code != test.status || record.path != "" {
			t.Fatalf("%s：响应 = %d，上游请求 = %#v", name, recorder.Code, record)
		}
	}
}

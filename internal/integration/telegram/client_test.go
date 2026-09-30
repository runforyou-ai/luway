package telegram

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/runforyou-ai/cervi/pkg/connectiontest"
)

const testBotToken = "123456:test_token"

// TestGetMe 验证 getMe 请求和机器人身份解析。
func TestGetMe(t *testing.T) {
	server := httptest.NewTestServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/bot"+testBotToken+"/getMe" {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "{}" {
			t.Fatalf("request body = %s, want {}", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ok":true,"result":{"id":987654321,"is_bot":true,"first_name":"Demo","last_name":"Support","username":"demo_support_bot"}}`))
	}))

	bot, err := NewClient(server.Client(), WithBaseURL(server.URL)).GetMe(context.Background(), testBotToken)
	if err != nil {
		t.Fatal(err)
	}
	if bot.ID != 987654321 || !bot.IsBot || bot.FirstName != "Demo" || bot.LastName != "Support" || bot.Username != "demo_support_bot" {
		t.Fatalf("unexpected bot: %#v", bot)
	}
}

// TestGetUserProfilePhoto 验证只返回最新头像中的最大静态尺寸。
func TestGetUserProfilePhoto(t *testing.T) {
	server := httptest.NewTestServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/bot"+testBotToken+"/getUserProfilePhotos" {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != `{"user_id":998877,"offset":0,"limit":1}` {
			t.Fatalf("request body = %s", body)
		}
		_, _ = writer.Write([]byte(`{"ok":true,"result":{"total_count":1,"photos":[[{"file_id":"small","file_unique_id":"same","width":160,"height":160},{"file_id":"large","file_unique_id":"same","width":640,"height":640}]]}}`))
	}))

	photo, err := NewClient(server.Client(), WithBaseURL(server.URL)).GetUserProfilePhoto(context.Background(), testBotToken, 998877)
	if err != nil {
		t.Fatal(err)
	}
	if photo == nil || photo.FileID != "large" || photo.UniqueID != "same" {
		t.Fatalf("profile photo = %#v", photo)
	}
}

// TestGetUserProfilePhotoWithoutPhoto 验证没有头像时返回 nil。
func TestGetUserProfilePhotoWithoutPhoto(t *testing.T) {
	server := httptest.NewTestServer(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"ok":true,"result":{"total_count":0,"photos":[]}}`))
	}))

	photo, err := NewClient(server.Client(), WithBaseURL(server.URL)).GetUserProfilePhoto(context.Background(), testBotToken, 998877)
	if err != nil || photo != nil {
		t.Fatalf("profile photo = %#v, error = %v", photo, err)
	}
}

// TestDownloadPhoto 验证 getFile 后按魔数返回头像内容。
func TestDownloadPhoto(t *testing.T) {
	content := []byte{0xff, 0xd8, 0xff, 0x01}
	server := httptest.NewTestServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/bot" + testBotToken + "/getFile":
			_, _ = writer.Write([]byte(`{"ok":true,"result":{"file_id":"avatar","file_size":4,"file_path":"photos/avatar.jpg"}}`))
		case "/file/bot" + testBotToken + "/photos/avatar.jpg":
			writer.Header().Set("Content-Type", "text/plain")
			_, _ = writer.Write(content)
		default:
			t.Fatalf("unexpected request path: %s", request.URL.Path)
		}
	}))

	downloaded, err := NewClient(server.Client(), WithBaseURL(server.URL)).DownloadPhoto(context.Background(), testBotToken, "avatar")
	if err != nil {
		t.Fatal(err)
	}
	if downloaded.ContentType != "image/jpeg" || !bytes.Equal(downloaded.Data, content) {
		t.Fatalf("downloaded photo = %#v", downloaded)
	}
}

// TestDownloadPhotoRejectsUnsafeContent 验证非法路径、非图片、超限和重定向均失败。
func TestDownloadPhotoRejectsUnsafeContent(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		content  []byte
		redirect bool
	}{
		{name: "parent path", filePath: "../avatar.jpg", content: []byte{0xff, 0xd8, 0xff}},
		{name: "encoded parent path", filePath: "photos/%2e%2e/avatar.jpg", content: []byte{0xff, 0xd8, 0xff}},
		{name: "absolute URL", filePath: "https://example.com/avatar.jpg", content: []byte{0xff, 0xd8, 0xff}},
		{name: "not image", filePath: "photos/avatar.jpg", content: []byte("not an image")},
		{name: "too large", filePath: "photos/avatar.jpg", content: bytes.Repeat([]byte{0xff}, maxAvatarSize+1)},
		{name: "redirect", filePath: "photos/avatar.jpg", redirect: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			followed := false
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/bot" + testBotToken + "/getFile":
					_, _ = writer.Write([]byte(`{"ok":true,"result":{"file_id":"avatar","file_path":"` + test.filePath + `"}}`))
				case "/file/bot" + testBotToken + "/photos/avatar.jpg":
					if test.redirect {
						http.Redirect(writer, request, "/followed", http.StatusFound)
						return
					}
					_, _ = writer.Write(test.content)
				case "/followed":
					followed = true
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()

			client := NewClient(connectiontest.NewHTTPClient(), WithBaseURL(server.URL))
			if _, err := client.DownloadPhoto(context.Background(), testBotToken, "avatar"); err == nil {
				t.Fatal("unsafe avatar download succeeded")
			}
			if followed {
				t.Fatal("avatar download followed redirect")
			}
		})
	}
}

// TestDownloadPhotoErrorDoesNotLeakTokenURL 验证头像传输错误的地址脱敏。
func TestDownloadPhotoErrorDoesNotLeakTokenURL(t *testing.T) {
	client := NewClient(httpDoerFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/bot"+testBotToken+"/getFile" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"file_id":"avatar","file_path":"photos/avatar.jpg"}}`)),
			}, nil
		}
		return nil, &url.Error{Op: "Get", URL: request.URL.String(), Err: context.DeadlineExceeded}
	}), WithBaseURL("https://api.telegram.org"))

	_, err := client.DownloadPhoto(context.Background(), testBotToken, "avatar")
	if err == nil || strings.Contains(err.Error(), testBotToken) || strings.Contains(err.Error(), "/file/bot") {
		t.Fatalf("unsafe avatar error = %v", err)
	}
}

// TestSetWebhook 验证注册参数固定限制为私聊消息和成员状态并丢弃积压 Update。
func TestSetWebhook(t *testing.T) {
	server := httptest.NewTestServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/bot"+testBotToken+"/setWebhook" {
			t.Fatalf("unexpected request path: %s", request.URL.Path)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		const expected = `{"url":"https://app.example/api/public/telegram-channels/channel-id/webhook","secret_token":"secret_token","allowed_updates":["message","my_chat_member"],"drop_pending_updates":true}`
		if string(body) != expected {
			t.Fatalf("request body = %s, want %s", body, expected)
		}
		_, _ = writer.Write([]byte(`{"ok":true,"result":true}`))
	}))

	err := NewClient(server.Client(), WithBaseURL(server.URL)).SetWebhook(
		context.Background(),
		testBotToken,
		Webhook{
			URL:    "https://app.example/api/public/telegram-channels/channel-id/webhook",
			Secret: "secret_token",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
}

// TestGetMeResponseFailures 验证 Telegram 响应异常会归类且不暴露原始响应。
func TestGetMeResponseFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		kind   connectiontest.FailureKind
	}{
		{name: "not a bot", status: http.StatusOK, body: `{"ok":true,"result":{"id":1,"is_bot":false,"first_name":"User"}}`, kind: connectiontest.FailureProtocol},
		{name: "malformed JSON", status: http.StatusOK, body: `{`, kind: connectiontest.FailureProtocol},
		{name: "ok false without code", status: http.StatusOK, body: `{"ok":false}`, kind: connectiontest.FailureProtocol},
		{name: "unauthorized", status: http.StatusOK, body: `{"ok":false,"error_code":401,"description":"token leaked in response"}`, kind: connectiontest.FailureUnauthorized},
		{name: "unauthorized status", status: http.StatusUnauthorized, body: `{"ok":false,"error_code":401,"description":"token leaked in response"}`, kind: connectiontest.FailureUnauthorized},
		{name: "forbidden status", status: http.StatusForbidden, body: `{"ok":false,"error_code":403,"description":"token leaked in response"}`, kind: connectiontest.FailureForbidden},
		{name: "rate limited", status: http.StatusOK, body: `{"ok":false,"error_code":429}`, kind: connectiontest.FailureRateLimited},
		{name: "rate limited status", status: http.StatusTooManyRequests, body: `{"ok":false,"error_code":429,"parameters":{"retry_after":3}}`, kind: connectiontest.FailureRateLimited},
		{name: "gateway error page", status: http.StatusBadGateway, body: `<html>token leaked in response</html>`, kind: connectiontest.FailureUnavailable},
		{name: "unauthorized without envelope", status: http.StatusUnauthorized, body: `token leaked in response`, kind: connectiontest.FailureUnauthorized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTestServer(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))

			_, err := NewClient(server.Client(), WithBaseURL(server.URL)).GetMe(context.Background(), testBotToken)
			_, kind, classified := connectiontest.Details(err)
			if !classified || kind != test.kind {
				t.Fatalf("error = %v, kind = %q, want %q", err, kind, test.kind)
			}
			if strings.Contains(err.Error(), "token leaked") || strings.Contains(err.Error(), testBotToken) {
				t.Fatalf("error contains secret data: %v", err)
			}
		})
	}
}

// TestClientRejectsInvalidTokenBeforeRequest 验证发起请求前的 Token 格式校验。
func TestClientRejectsInvalidTokenBeforeRequest(t *testing.T) {
	called := false
	client := NewClient(httpDoerFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unexpected request")
	}))
	_, err := client.GetMe(context.Background(), "invalid token")
	_, kind, classified := connectiontest.Details(err)
	if !classified || kind != connectiontest.FailureInvalidConfig {
		t.Fatalf("error = %v, kind = %q, want invalid_config", err, kind)
	}
	if called {
		t.Fatal("invalid token triggered a request")
	}
}

// TestTransportErrorDoesNotLeakTokenURL 验证传输错误剥离包含 Token 的请求地址。
func TestTransportErrorDoesNotLeakTokenURL(t *testing.T) {
	client := NewClient(httpDoerFunc(func(*http.Request) (*http.Response, error) {
		return nil, &url.Error{
			Op:  "Post",
			URL: "https://api.telegram.org/bot" + testBotToken + "/getMe",
			Err: context.DeadlineExceeded,
		}
	}))
	_, err := client.GetMe(context.Background(), testBotToken)
	_, kind, classified := connectiontest.Details(err)
	if !classified || kind != connectiontest.FailureTimeout {
		t.Fatalf("error = %v, kind = %q, want timeout", err, kind)
	}
	if strings.Contains(err.Error(), testBotToken) || strings.Contains(err.Error(), "api.telegram.org/bot") {
		t.Fatalf("error contains Telegram request URL: %v", err)
	}
}

type httpDoerFunc func(*http.Request) (*http.Response, error)

// Do 调用测试函数。
func (f httpDoerFunc) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

// TestDownloadMedia 验证媒体取回每次重新调用 getFile 并按上限读取内容。
func TestDownloadMedia(t *testing.T) {
	var getFileCalls int
	server := httptest.NewTestServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/bot" + testBotToken + "/getFile":
			getFileCalls++
			body, _ := io.ReadAll(request.Body)
			if string(body) != `{"file_id":"media-1"}` {
				t.Fatalf("request body = %s", body)
			}
			_, _ = writer.Write([]byte(`{"ok":true,"result":{"file_id":"media-1","file_unique_id":"u","file_size":9,"file_path":"documents/file_1.pdf"}}`))
		case "/file/bot" + testBotToken + "/documents/file_1.pdf":
			_, _ = writer.Write([]byte("PDF-BYTES"))
		default:
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
	}))
	client := NewClient(server.Client(), WithBaseURL(server.URL))
	data, err := client.DownloadMedia(context.Background(), testBotToken, "media-1", 1<<20)
	if err != nil || string(data) != "PDF-BYTES" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if _, err := client.DownloadMedia(context.Background(), testBotToken, "media-1", 8); err == nil {
		t.Fatal("oversized media accepted")
	} else if _, kind, _ := connectiontest.Details(err); kind != connectiontest.FailureProtocol {
		t.Fatalf("oversized kind=%s", kind)
	}
	if getFileCalls != 2 {
		t.Fatalf("getFile calls=%d", getFileCalls)
	}
}

// TestDownloadMediaRejected 验证平台拒绝文件时返回协议错误而不是传输错误。
func TestDownloadMediaRejected(t *testing.T) {
	server := httptest.NewTestServer(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: file is too big"}`))
	}))
	_, err := NewClient(server.Client(), WithBaseURL(server.URL)).DownloadMedia(context.Background(), testBotToken, "media-2", 1<<20)
	if err == nil {
		t.Fatal("rejected media accepted")
	}
	if _, kind, ok := connectiontest.Details(err); !ok || kind == connectiontest.FailureUnavailable || kind == connectiontest.FailureTimeout || kind == connectiontest.FailureNetwork {
		t.Fatalf("kind=%s ok=%t", kind, ok)
	}
}

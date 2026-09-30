package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestSendTextOutcomes 验证 Telegram 发送结果分类和纯文本请求。
func TestSendTextOutcomes(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		response string
		code     string
		retry    time.Duration
	}{
		{"success", 200, `{"ok":true,"result":{"message_id":42,"chat":{"id":123}}}`, "", 0},
		{"rate limit", 429, `{"ok":false,"error_code":429,"parameters":{"retry_after":7}}`, "rate_limited", 7 * time.Second},
		{"blocked", 403, `{"ok":false,"error_code":403}`, "recipient_unavailable", 0},
		{"token", 401, `{"ok":false,"error_code":401}`, "invalid_token", 0},
		{"rejected", 400, `{"ok":false,"error_code":400}`, "message_rejected", 0},
		{"ambiguous server error", 502, `{"ok":false,"error_code":502}`, "unknown_result", 0},
		{"invalid JSON", 200, `broken response`, "unknown_result", 0},
		{"wrong chat", 200, `{"ok":true,"result":{"message_id":42,"chat":{"id":999}}}`, "unknown_result", 0},
		{"missing message", 200, `{"ok":true,"result":{"chat":{"id":123}}}`, "unknown_result", 0},
		{"missing retry delay", 429, `{"ok":false,"error_code":429}`, "unknown_result", 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/sendMessage") || body["chat_id"] != float64(123) || body["text"] != "你好 *plain*" || len(body) != 2 {
					t.Errorf("unexpected request: %v", body)
				}
				w.WriteHeader(test.status)
				_, _ = fmt.Fprint(w, test.response)
			}))
			defer server.Close()
			id, err := NewClient(server.Client(), WithBaseURL(server.URL)).SendText(context.Background(), testBotToken, TextMessage{ChatID: "123", Body: "你好 *plain*"})
			if test.code == "" {
				if err != nil || id != 42 {
					t.Fatalf("id=%d err=%v", id, err)
				}
				return
			}
			var failure *SendError
			if !errors.As(err, &failure) || failure.Code != test.code || failure.RetryAfter != test.retry {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Contains(err.Error(), testBotToken) {
				t.Fatal("token leaked")
			}
		})
	}
}

// TestSendTextNetworkFailure 验证传输失败时的 Token 脱敏和单次请求。
func TestSendTextNetworkFailure(t *testing.T) {
	client := NewClient(httpDoerFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("network error with %s", testBotToken)
	}))
	_, err := client.SendText(context.Background(), testBotToken, TextMessage{ChatID: "123", Body: "hello"})
	var failure *SendError
	if !errors.As(err, &failure) || failure.Code != "unknown_result" || strings.Contains(err.Error(), testBotToken) {
		t.Fatalf("error=%v", err)
	}
}

// TestSendTextReplyParameters 验证整条消息引用显式禁止丢弃引用发送。
func TestSendTextReplyParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Reply map[string]any `json:"reply_parameters"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Reply) != 2 || payload.Reply["message_id"] != float64(7) || payload.Reply["allow_sending_without_reply"] != false {
			t.Errorf("reply=%v", payload.Reply)
		}
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{"message_id":42,"chat":{"id":123}}}`)
	}))
	defer server.Close()
	target := "7"
	if _, err := NewClient(server.Client(), WithBaseURL(server.URL)).SendText(context.Background(), testBotToken, TextMessage{ChatID: "123", Body: "回答", ReplyMessageID: &target}); err != nil {
		t.Fatal(err)
	}
}

// TestSendTextInvalidReplyIdentifier 验证 Telegram 适配层拒绝无法转换的平台引用编号。
func TestSendTextInvalidReplyIdentifier(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid reference reached Telegram")
	}))
	defer server.Close()
	for _, target := range []string{"message:opaque", "0", "-1", "9223372036854775808"} {
		_, err := NewClient(server.Client(), WithBaseURL(server.URL)).SendText(context.Background(), testBotToken, TextMessage{ChatID: "123", Body: "回答", ReplyMessageID: &target})
		var failure *SendError
		if !errors.As(err, &failure) || failure.Code != "invalid_message" {
			t.Fatalf("target=%s err=%v", target, err)
		}
	}
}

// opusVoice 是以 OpusHead 负载开头的最小 Ogg 首页。
var opusVoice = "OggS" + strings.Repeat("\x00", 22) + "\x01\x13" + "OpusHead" + strings.Repeat("\x00", 11)

// TestSendMediaRequest 验证媒体按内容选择接口，并以 multipart 携带原始文件名、说明和引用。
func TestSendMediaRequest(t *testing.T) {
	cases := []struct {
		contentType   string
		content       string
		byteSize      int64
		width, height int
		method, field string
	}{
		{"image/jpeg", "content", 7, 800, 600, "sendPhoto", "photo"},
		{"image/png", "content", 7, 0, 0, "sendPhoto", "photo"},
		{"image/png", "content", 7, 400, 9000, "sendDocument", "document"},
		{"image/jpeg", "content", photoByteLimit + 1, 800, 600, "sendDocument", "document"},
		{"image/gif", "content", 7, 320, 200, "sendAnimation", "animation"},
		{"image/svg+xml", "content", 7, 0, 0, "sendDocument", "document"},
		{"video/mp4", "content", 7, 0, 0, "sendVideo", "video"},
		{"audio/ogg", opusVoice, int64(len(opusVoice)), 0, 0, "sendVoice", "voice"},
		{"audio/ogg", "OggS" + strings.Repeat("\x00", 24) + "\x01vorbis", 35, 0, 0, "sendDocument", "document"},
		{"audio/mpeg", "content", 7, 0, 0, "sendAudio", "audio"},
		{"application/pdf", "content", 7, 0, 0, "sendDocument", "document"},
	}
	for index, tc := range cases {
		t.Run(fmt.Sprintf("%d/%s/%s", index, tc.contentType, tc.method), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/bot"+testBotToken+"/"+tc.method {
					t.Errorf("path=%s", r.URL.Path)
				}
				reader, err := r.MultipartReader()
				if err != nil {
					t.Fatal(err)
				}
				fields := map[string]string{}
				for {
					part, err := reader.NextPart()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					data, _ := io.ReadAll(part)
					fields[part.Header.Get("Content-Disposition")] = string(data)
					if part.FormName() == tc.field && part.Header.Get("Content-Type") != tc.contentType {
						t.Errorf("part type=%s", part.Header.Get("Content-Type"))
					}
				}
				// 文件名以原始 UTF-8 写入带引号的 filename 参数。
				want := map[string]string{
					`form-data; name="chat_id"`: "123", `form-data; name="caption"`: "请查收",
					`form-data; name="reply_parameters"`:                           `{"message_id":7,"allow_sending_without_reply":false}`,
					`form-data; name="` + tc.field + `"; filename="报价 \"终版\".pdf"`: tc.content,
				}
				if len(fields) != len(want) {
					t.Errorf("fields=%q", fields)
				}
				for disposition, value := range want {
					if fields[disposition] != value {
						t.Errorf("disposition=%s fields=%q", disposition, fields)
					}
				}
				_, _ = fmt.Fprint(w, `{"ok":true,"result":{"message_id":42,"chat":{"id":123}}}`)
			}))
			defer server.Close()
			target := "7"
			messageID, err := NewClient(server.Client(), WithBaseURL(server.URL)).SendMedia(context.Background(), testBotToken, MediaMessage{
				ChatID: "123", FileName: `报价 "终版".pdf`, ContentType: tc.contentType, ByteSize: tc.byteSize, ImageWidth: tc.width, ImageHeight: tc.height,
				Content: strings.NewReader(tc.content), Caption: "请查收", ReplyMessageID: &target,
			})
			if err != nil || messageID != 42 {
				t.Fatalf("messageID=%d err=%v", messageID, err)
			}
		})
	}
}

// TestSendMediaOptionalFields 验证没有说明和引用时表单只携带聊天编号与文件。
func TestSendMediaOptionalFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if len(r.MultipartForm.Value) != 1 || r.FormValue("chat_id") != "123" || len(r.MultipartForm.File["document"]) != 1 {
			t.Errorf("form=%+v", r.MultipartForm)
		}
		_, _ = fmt.Fprint(w, `{"ok":true,"result":{"message_id":42,"chat":{"id":123}}}`)
	}))
	defer server.Close()
	if _, err := NewClient(server.Client(), WithBaseURL(server.URL)).SendMedia(context.Background(), testBotToken, MediaMessage{ChatID: "123", FileName: "a.pdf", ContentType: "application/pdf", Content: strings.NewReader("content")}); err != nil {
		t.Fatal(err)
	}
}

// failingContent 在给出部分内容后返回读取错误。
type failingContent struct{ sent bool }

// Read 首次返回部分内容，随后返回存储读取错误。
func (c *failingContent) Read(buffer []byte) (int, error) {
	if c.sent {
		return 0, errors.New("storage interrupted")
	}
	c.sent = true
	return copy(buffer, "partial"), nil
}

// TestSendMediaContentFailure 验证内容读取中断归类为附件不可读，平台结果不标记为未知。
func TestSendMediaContentFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	defer server.Close()
	_, err := NewClient(server.Client(), WithBaseURL(server.URL)).SendMedia(context.Background(), testBotToken, MediaMessage{ChatID: "123", FileName: "a.pdf", ContentType: "application/pdf", Content: &failingContent{}})
	var failure *SendError
	if !errors.As(err, &failure) || failure.Code != "attachment_unavailable" {
		t.Fatalf("err=%v", err)
	}
}

// TestSendMediaOutcomes 验证媒体发送沿用平台拒绝与结果未知的分类。
func TestSendMediaOutcomes(t *testing.T) {
	for status, code := range map[int]string{400: "message_rejected", 403: "recipient_unavailable", 502: "unknown_result"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"ok":false,"error_code":%d}`, status)
		}))
		_, err := NewClient(server.Client(), WithBaseURL(server.URL)).SendMedia(context.Background(), testBotToken, MediaMessage{ChatID: "123", FileName: "a.pdf", ContentType: "application/pdf", Content: strings.NewReader("content")})
		server.Close()
		var failure *SendError
		if !errors.As(err, &failure) || failure.Code != code {
			t.Fatalf("status=%d err=%v", status, err)
		}
	}
	// 网络失败无法确认平台是否受理。
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client := NewClient(server.Client(), WithBaseURL(server.URL))
	server.Close()
	_, err := client.SendMedia(context.Background(), testBotToken, MediaMessage{ChatID: "123", FileName: "a.pdf", ContentType: "application/pdf", Content: strings.NewReader("content")})
	var failure *SendError
	if !errors.As(err, &failure) || failure.Code != "unknown_result" {
		t.Fatalf("err=%v", err)
	}
}

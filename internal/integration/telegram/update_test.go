package telegram

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestNormalizeMessageReply 验证机器人原文、跨聊天引用与部分引用的整条语义。
func TestNormalizeMessageReply(t *testing.T) {
	for _, test := range []struct {
		name string
		chat int64
		want bool
	}{
		{"same chat bot reply", 123, true}, {"another chat", 456, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var message updateMessage
			body := fmt.Sprintf(`{"message_id":9,"date":1788880000,"text":"客户回答","chat":{"id":123,"type":"private"},"from":{"id":123,"first_name":"客户"},"quote":{"text":"部分"},"reply_to_message":{"message_id":7,"text":"完整原文","chat":{"id":%d,"type":"private"},"from":{"id":999,"is_bot":true,"first_name":"客服 Bot"}}}`, test.chat)
			if err := json.Unmarshal([]byte(body), &message); err != nil {
				t.Fatal(err)
			}
			result, reason := normalizeMessage(message)
			if result == nil || reason != "" || (result.Reply != nil) != test.want {
				t.Fatalf("result=%+v reason=%s", result, reason)
			}
			if test.want && (result.Reply.MessageID != 7 || result.Reply.Body != "完整原文" || !result.Reply.SenderIsBot || result.Reply.SenderName != "客服 Bot") {
				t.Fatalf("reply=%+v", result.Reply)
			}
		})
	}
}

// TestNormalizeMessageMedia 验证七类媒体的解析、默认文件名与类型、照片档位选择和说明校验。
func TestNormalizeMessageMedia(t *testing.T) {
	envelope := func(payload string) string {
		return `{"message_id":9,"date":1788880000,"chat":{"id":123,"type":"private"},"from":{"id":123,"first_name":"客户"},` + payload + `}`
	}
	for _, test := range []struct {
		name   string
		body   string
		reason string
		media  *InboundMedia
		text   string
	}{
		{
			name: "photo picks largest size", body: envelope(`"caption":" 看图 ","photo":[{"file_id":"small","file_unique_id":"u1","width":90,"height":60,"file_size":100},{"file_id":"large","file_unique_id":"u1","width":800,"height":600,"file_size":90000},{"file_id":"broken","file_unique_id":"u1","width":0,"height":0}]`),
			media: &InboundMedia{FileID: "large", UniqueID: "u1", FileName: "photo.jpg", ContentType: "image/jpeg", ByteSize: 90000, Width: 800, Height: 600}, text: "看图",
		},
		{
			name: "document keeps name and type", body: envelope(`"document":{"file_id":"doc","file_unique_id":"u2","file_name":"合同.pdf","mime_type":"application/pdf","file_size":2048}`),
			media: &InboundMedia{FileID: "doc", UniqueID: "u2", FileName: "合同.pdf", ContentType: "application/pdf", ByteSize: 2048},
		},
		{
			name: "animation wins over document", body: envelope(`"document":{"file_id":"doc","file_unique_id":"u3","file_name":"x.gif.mp4","mime_type":"video/mp4"},"animation":{"file_id":"anim","file_unique_id":"u3","width":320,"height":240,"file_size":500}`),
			media: &InboundMedia{FileID: "anim", UniqueID: "u3", FileName: "animation.mp4", ContentType: "video/mp4", ByteSize: 500},
		},
		{
			name: "voice default name", body: envelope(`"voice":{"file_id":"voice","file_unique_id":"u4","mime_type":"audio/ogg","file_size":7}`),
			media: &InboundMedia{FileID: "voice", UniqueID: "u4", FileName: "voice.ogg", ContentType: "audio/ogg", ByteSize: 7},
		},
		{
			name: "video note default type", body: envelope(`"video_note":{"file_id":"note","file_unique_id":"u5","length":240,"file_size":9}`),
			media: &InboundMedia{FileID: "note", UniqueID: "u5", FileName: "video-note.mp4", ContentType: "video/mp4", ByteSize: 9},
		},
		{
			name: "video dimensions are not image size", body: envelope(`"video":{"file_id":"video","file_unique_id":"u6","width":1920,"height":1080,"mime_type":"video/mp4","file_name":"clip.mp4"}`),
			media: &InboundMedia{FileID: "video", UniqueID: "u6", FileName: "clip.mp4", ContentType: "video/mp4"},
		},
		{
			name: "audio default name", body: envelope(`"audio":{"file_id":"audio","file_unique_id":"u7","mime_type":"audio/mpeg","file_size":11}`),
			media: &InboundMedia{FileID: "audio", UniqueID: "u7", FileName: "audio.mp3", ContentType: "audio/mpeg", ByteSize: 11},
		},
		{name: "sticker unsupported", body: envelope(`"sticker":{"file_id":"sticker","file_unique_id":"u8"}`), reason: "unsupported_content"},
		{name: "missing unique id", body: envelope(`"document":{"file_id":"doc"}`), reason: "invalid_media"},
		{name: "caption too long", body: envelope(`"caption":"` + strings.Repeat("长", 1025) + `","document":{"file_id":"doc","file_unique_id":"u9"}`), reason: "invalid_caption"},
		{name: "text ignores media", body: envelope(`"text":"你好","photo":[{"file_id":"p","file_unique_id":"u10","width":1,"height":1}]`), text: "你好"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var message updateMessage
			if err := json.Unmarshal([]byte(test.body), &message); err != nil {
				t.Fatal(err)
			}
			result, reason := normalizeMessage(message)
			if reason != test.reason {
				t.Fatalf("reason=%q want %q", reason, test.reason)
			}
			if test.reason != "" {
				return
			}
			if result.Body != test.text {
				t.Fatalf("body=%q want %q", result.Body, test.text)
			}
			if (result.Media == nil) != (test.media == nil) || (test.media != nil && *result.Media != *test.media) {
				t.Fatalf("media=%+v want %+v", result.Media, test.media)
			}
		})
	}
}

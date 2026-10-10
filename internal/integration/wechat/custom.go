package wechat

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"github.com/runforyou-ai/luway/pkg/httpjson"
)

// MediaType 是临时素材与客服消息的素材类型。
type MediaType string

const (
	MediaTypeImage MediaType = "image"
	MediaTypeVoice MediaType = "voice"
)

// CustomMessage 是一条客服消息：Text 不为空时发送文本，否则按 MediaType 发送已上传的临时素材 MediaID。
type CustomMessage struct {
	ToUser    string
	Text      string
	MediaType MediaType
	MediaID   string
}

// 客服消息与临时素材接口的业务错误码。
const (
	// codeReplyTimeLimit 表示超出回复时限。
	codeReplyTimeLimit = 45015
	// codeReplyCountLimit 表示超出窗口内可发送的条数。
	codeReplyCountLimit = 45047
	// codeUnsubscribed 表示接收者未关注公众号。
	codeUnsubscribed = 43004
	// codeInvalidOpenID 表示接收者编号无效。
	codeInvalidOpenID = 40003
	// codeContentTooLong 表示消息内容超过长度限制。
	codeContentTooLong = 45002
	// codeRateLimited 表示接口调用过于频繁。
	codeRateLimited = 45011
	// codeDailyLimit 表示接口当日调用次数达到上限。
	codeDailyLimit = 45009
	// codeSystemBusy 表示微信系统繁忙。
	codeSystemBusy = -1
)

// ReplyWindowClosed 判断错误是否为超出客服消息的回复时限或条数。
func (e *APIError) ReplyWindowClosed() bool {
	return e.Code == codeReplyTimeLimit || e.Code == codeReplyCountLimit
}

// RecipientUnavailable 判断错误是否为接收者未关注公众号或编号无效。
func (e *APIError) RecipientUnavailable() bool {
	return e.Code == codeUnsubscribed || e.Code == codeInvalidOpenID
}

// MessageRejected 判断错误是否为消息内容不被接受。
func (e *APIError) MessageRejected() bool {
	return e.Code == codeContentTooLong
}

// Busy 判断错误是否为调用频率或系统繁忙，稍后重发可以成功。
func (e *APIError) Busy() bool {
	return e.Code == codeRateLimited || e.Code == codeDailyLimit || e.Code == codeSystemBusy
}

// SendCustomMessage 用接口调用凭据发送一条客服消息。
func (c *Client) SendCustomMessage(ctx context.Context, accessToken string, message CustomMessage) error {
	payload := map[string]any{"touser": message.ToUser}
	if message.Text != "" {
		payload["msgtype"] = "text"
		payload["text"] = map[string]string{"content": message.Text}
	} else {
		payload["msgtype"] = string(message.MediaType)
		payload[string(message.MediaType)] = map[string]string{"media_id": message.MediaID}
	}
	return c.post(ctx, "/cgi-bin/message/custom/send?access_token="+url.QueryEscape(accessToken), payload, &struct{}{})
}

// UploadMedia 用接口调用凭据把 content 上传为 mediaType 类型的临时素材，返回素材编号。
func (c *Client) UploadMedia(ctx context.Context, accessToken string, mediaType MediaType, fileName, contentType string, content io.Reader) (string, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="media"; filename=%q`, fileName))
	header.Set("Content-Type", contentType)
	part, err := form.CreatePart(header)
	if err != nil {
		return "", fmt.Errorf("create wechat media part: %w", err)
	}
	if _, err := io.Copy(part, content); err != nil {
		return "", fmt.Errorf("read wechat media content: %w", err)
	}
	if err := form.Close(); err != nil {
		return "", fmt.Errorf("close wechat media form: %w", err)
	}
	address := c.baseURL + "/cgi-bin/media/upload?access_token=" + url.QueryEscape(accessToken) + "&type=" + url.QueryEscape(string(mediaType))
	var result struct {
		MediaID string `json:"media_id"`
	}
	if err := call(ctx, c.media, httpjson.Request{Method: http.MethodPost, URL: address, Body: &body, ContentType: form.FormDataContentType()}, &result); err != nil {
		return "", err
	}
	if strings.TrimSpace(result.MediaID) == "" {
		return "", fmt.Errorf("%w: media id missing", ErrUnavailable)
	}
	return result.MediaID, nil
}

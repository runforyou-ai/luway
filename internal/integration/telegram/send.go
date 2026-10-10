package telegram

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// SendError 保留可安全记录的发送分类和平台等待时间。
type SendError struct {
	Code       string
	RetryAfter time.Duration
}

// Error 返回不含 Token 和平台响应正文的错误码。
func (e *SendError) Error() string { return e.Code }

// TextMessage 定义纯文本投递及其可选引用目标。
type TextMessage struct {
	ChatID         string
	Body           string
	ReplyMessageID *string
}

// MediaMessage 定义单个文件的媒体投递、说明及其可选引用目标。
type MediaMessage struct {
	ChatID         string
	FileName       string
	ContentType    string
	ByteSize       int64
	ImageWidth     int
	ImageHeight    int
	Content        io.Reader
	Caption        string
	ReplyMessageID *string
}

// Sender 定义私聊文本与媒体投递依赖。
type Sender interface {
	SendText(context.Context, string, TextMessage) (int64, error)
	SendMedia(context.Context, string, MediaMessage) (int64, error)
}

// replyParameters 定义发送类接口的引用目标。
type replyParameters struct {
	MessageID                int64 `json:"message_id"`
	AllowSendingWithoutReply bool  `json:"allow_sending_without_reply"`
}

// SendText 发送纯文本并保留平台拒绝与结果未知的区别。
func (c *Client) SendText(ctx context.Context, token string, message TextMessage) (int64, error) {
	chat, reply, err := resolveSendTarget(token, message.ChatID, message.ReplyMessageID)
	if err != nil {
		return 0, err
	}
	payload, err := json.Marshal(struct {
		ChatID int64            `json:"chat_id"`
		Text   string           `json:"text"`
		Reply  *replyParameters `json:"reply_parameters,omitempty"`
	}{chat, message.Body, reply})
	if err != nil {
		return 0, &SendError{Code: "invalid_message"}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+token+"/sendMessage", bytes.NewReader(payload))
	if err != nil {
		return 0, &SendError{Code: "invalid_token"}
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return 0, &SendError{Code: "unknown_result"}
	}
	defer response.Body.Close()
	return readSendResult(response, chat)
}

// SendMedia 按内容类型选择媒体接口，以 multipart 流式上传单个文件。
func (c *Client) SendMedia(ctx context.Context, token string, message MediaMessage) (int64, error) {
	chat, reply, err := resolveSendTarget(token, message.ChatID, message.ReplyMessageID)
	if err != nil {
		return 0, err
	}
	// 预读首个 Ogg 页用于识别编码，其余内容仍随请求流式读取。
	content := bufio.NewReaderSize(message.Content, 4096)
	head, _ := content.Peek(36)
	method, field := mediaMethod(message, head)
	body, bodyWriter := io.Pipe()
	form := multipart.NewWriter(bodyWriter)
	written := make(chan error, 1)
	// 表单随请求读取同步写入管道，文件内容不整体载入内存。
	go func() {
		err := form.WriteField("chat_id", strconv.FormatInt(chat, 10))
		if err == nil && message.Caption != "" {
			err = form.WriteField("caption", message.Caption)
		}
		if err == nil && reply != nil {
			var encoded []byte
			if encoded, err = json.Marshal(reply); err == nil {
				err = form.WriteField("reply_parameters", string(encoded))
			}
		}
		if err == nil {
			header := textproto.MIMEHeader{}
			header.Set("Content-Disposition", multipart.FileContentDisposition(field, message.FileName))
			header.Set("Content-Type", message.ContentType)
			var part io.Writer
			if part, err = form.CreatePart(header); err == nil {
				_, err = io.Copy(part, content)
			}
		}
		if err == nil {
			err = form.Close()
		}
		bodyWriter.CloseWithError(err)
		written <- err
	}()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+token+"/"+method, body)
	if err != nil {
		body.Close()
		<-written
		return 0, &SendError{Code: "invalid_token"}
	}
	request.Header.Set("Content-Type", form.FormDataContentType())
	response, err := c.httpClient.Do(request)
	// 关闭管道读端使写入协程结束，再按其结果区分内容读取失败与传输失败。
	body.Close()
	writeErr := <-written
	if err != nil {
		// 内容读取中断时表单没有结束边界，平台无法受理该请求。
		if writeErr != nil && !errors.Is(writeErr, io.ErrClosedPipe) && ctx.Err() == nil {
			return 0, &SendError{Code: "attachment_unavailable"}
		}
		return 0, &SendError{Code: "unknown_result"}
	}
	defer response.Body.Close()
	return readSendResult(response, chat)
}

// photoByteLimit 是照片接口接受的单个文件字节上限。
const photoByteLimit = 10 << 20

// mediaMethod 返回附件对应的发送接口和文件字段，专用接口限制之外的内容按文件发送。
func mediaMethod(message MediaMessage, head []byte) (string, string) {
	mediaType, _, _ := mime.ParseMediaType(message.ContentType)
	width, height := message.ImageWidth, message.ImageHeight
	switch strings.ToLower(mediaType) {
	case "image/jpeg", "image/png", "image/webp":
		// 照片接口要求不超过 10 MiB、宽高之和不超过 10000 且宽高比不超过 20。
		if message.ByteSize > photoByteLimit || (width > 0 && height > 0 && (width+height > 10000 || width > 20*height || height > 20*width)) {
			return "sendDocument", "document"
		}
		return "sendPhoto", "photo"
	case "image/gif":
		return "sendAnimation", "animation"
	case "video/mp4":
		return "sendVideo", "video"
	case "audio/ogg":
		// 语音接口要求 Opus 编码，首个 Ogg 页的负载以 OpusHead 开头。
		if len(head) >= 36 && string(head[:4]) == "OggS" && string(head[28:36]) == "OpusHead" {
			return "sendVoice", "voice"
		}
		return "sendDocument", "document"
	case "audio/mpeg", "audio/mp4", "audio/x-m4a":
		return "sendAudio", "audio"
	default:
		return "sendDocument", "document"
	}
}

// resolveSendTarget 校验机器人凭据并解析聊天编号与可选引用目标。
func resolveSendTarget(token, chatID string, replyMessageID *string) (int64, *replyParameters, error) {
	if !botTokenPattern.MatchString(token) {
		return 0, nil, &SendError{Code: "invalid_token"}
	}
	chat, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil || chat <= 0 {
		return 0, nil, &SendError{Code: "invalid_recipient"}
	}
	if replyMessageID == nil {
		return chat, nil, nil
	}
	messageID, err := strconv.ParseInt(*replyMessageID, 10, 64)
	if err != nil || messageID <= 0 {
		return 0, nil, &SendError{Code: "invalid_message"}
	}
	return chat, &replyParameters{MessageID: messageID}, nil
}

// readSendResult 解析发送类接口的平台回执，区分平台拒绝与结果未知。
func readSendResult(response *http.Response, chat int64) (int64, error) {
	var envelope struct {
		OK         bool `json:"ok"`
		ErrorCode  int  `json:"error_code"`
		Parameters struct {
			RetryAfter int64 `json:"retry_after"`
		} `json:"parameters"`
		Result struct {
			MessageID int64 `json:"message_id"`
			Chat      struct {
				ID int64 `json:"id"`
			} `json:"chat"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseSize)).Decode(&envelope); err != nil {
		return 0, &SendError{Code: "unknown_result"}
	}
	if response.StatusCode >= 500 {
		return 0, &SendError{Code: "unknown_result"}
	}
	if !envelope.OK {
		switch envelope.ErrorCode {
		case 429:
			if envelope.Parameters.RetryAfter > 0 && envelope.Parameters.RetryAfter <= int64((1<<63-1)/time.Second) {
				return 0, &SendError{Code: "rate_limited", RetryAfter: time.Duration(envelope.Parameters.RetryAfter) * time.Second}
			}
		case 401:
			return 0, &SendError{Code: "invalid_token"}
		case 403:
			return 0, &SendError{Code: "recipient_unavailable"}
		case 400:
			return 0, &SendError{Code: "message_rejected"}
		}
		return 0, &SendError{Code: "unknown_result"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || envelope.Result.MessageID <= 0 || envelope.Result.Chat.ID != chat {
		return 0, &SendError{Code: "unknown_result"}
	}
	return envelope.Result.MessageID, nil
}

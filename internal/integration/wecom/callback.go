package wecom

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ChatType 标识回调所在会话类型。
type ChatType string

// 回调所在会话类型：单聊与群聊。
const (
	ChatTypeSingle ChatType = "single"
	ChatTypeGroup  ChatType = "group"
)

// MessageType 标识归一化后的入站消息类型。
type MessageType string

const (
	MessageTypeText        MessageType = "text"
	MessageTypeVoice       MessageType = "voice"
	MessageTypeImage       MessageType = "image"
	MessageTypeFile        MessageType = "file"
	MessageTypeVideo       MessageType = "video"
	MessageTypeMixed       MessageType = "mixed"
	MessageTypeUnsupported MessageType = "unsupported"
)

// MediaKind 标识入站媒体类型。
type MediaKind string

const (
	MediaKindImage MediaKind = "image"
	MediaKindFile  MediaKind = "file"
	MediaKindVideo MediaKind = "video"
)

// Media 是入站媒体的加密下载地址与解密密钥，地址五分钟内有效。
type Media struct {
	Kind   MediaKind
	URL    string
	AESKey string
}

// Message 是归一化后的入站消息；语音为平台转写的文本。
type Message struct {
	Type  MessageType
	Text  string
	Media []Media
}

// Event 是入站事件，模板卡片事件携带按钮与任务标识。
type Event struct {
	Type     string
	EventKey string
	TaskID   string
}

// Callback 是一次消息或事件回调，MsgID 用于排重。
type Callback struct {
	ReqID     string
	MsgID     string
	BotID     string
	ChatType  ChatType
	ChatID    string
	UserID    string
	CreatedAt time.Time
	Message   *Message
	Event     *Event
}

// callbackMedia 是回调中的媒体结构。
type callbackMedia struct {
	URL    string `json:"url"`
	AESKey string `json:"aeskey"`
}

// callbackText 是回调中的文本结构。
type callbackText struct {
	Content string `json:"content"`
}

// callbackBody 是消息与事件回调的公共正文。
type callbackBody struct {
	MsgID      string   `json:"msgid"`
	AIBotID    string   `json:"aibotid"`
	ChatID     string   `json:"chatid"`
	ChatType   ChatType `json:"chattype"`
	CreateTime int64    `json:"create_time"`
	From       struct {
		UserID string `json:"userid"`
	} `json:"from"`
	MsgType string         `json:"msgtype"`
	Text    *callbackText  `json:"text"`
	Voice   *callbackText  `json:"voice"`
	Image   *callbackMedia `json:"image"`
	File    *callbackMedia `json:"file"`
	Video   *callbackMedia `json:"video"`
	Mixed   *struct {
		Items []struct {
			MsgType string         `json:"msgtype"`
			Text    *callbackText  `json:"text"`
			Image   *callbackMedia `json:"image"`
		} `json:"msg_item"`
	} `json:"mixed"`
	Event *struct {
		EventType string `json:"eventtype"`
		EventKey  string `json:"event_key"`
		TaskID    string `json:"task_id"`
	} `json:"event"`
}

// parseCallback 把消息或事件回调帧归一化为 Callback。
func parseCallback(received frame) (Callback, error) {
	var body callbackBody
	if err := json.Unmarshal(received.Body, &body); err != nil {
		return Callback{}, err
	}
	callback := Callback{
		ReqID:    received.Headers.ReqID,
		MsgID:    body.MsgID,
		BotID:    body.AIBotID,
		ChatType: body.ChatType,
		ChatID:   body.ChatID,
		UserID:   body.From.UserID,
	}
	if body.CreateTime > 0 {
		callback.CreatedAt = time.Unix(body.CreateTime, 0)
	}
	if received.Cmd == cmdEventCallback {
		if body.Event == nil {
			return Callback{}, errors.New("wecom event callback without event")
		}
		callback.Event = &Event{Type: body.Event.EventType, EventKey: body.Event.EventKey, TaskID: body.Event.TaskID}
		return callback, nil
	}
	callback.Message = new(normalizeMessage(body))
	return callback, nil
}

// normalizeMessage 按消息类型提取文本与媒体，结构缺失时归为不支持。
func normalizeMessage(body callbackBody) Message {
	unsupported := Message{Type: MessageTypeUnsupported}
	switch body.MsgType {
	case "text":
		if body.Text == nil {
			return unsupported
		}
		return Message{Type: MessageTypeText, Text: body.Text.Content}
	case "voice":
		if body.Voice == nil {
			return unsupported
		}
		return Message{Type: MessageTypeVoice, Text: body.Voice.Content}
	case "image":
		if body.Image == nil {
			return unsupported
		}
		return Message{Type: MessageTypeImage, Media: []Media{{Kind: MediaKindImage, URL: body.Image.URL, AESKey: body.Image.AESKey}}}
	case "file":
		if body.File == nil {
			return unsupported
		}
		return Message{Type: MessageTypeFile, Media: []Media{{Kind: MediaKindFile, URL: body.File.URL, AESKey: body.File.AESKey}}}
	case "video":
		if body.Video == nil {
			return unsupported
		}
		return Message{Type: MessageTypeVideo, Media: []Media{{Kind: MediaKindVideo, URL: body.Video.URL, AESKey: body.Video.AESKey}}}
	case "mixed":
		if body.Mixed == nil {
			return unsupported
		}
		message := Message{Type: MessageTypeMixed}
		var texts []string
		for _, item := range body.Mixed.Items {
			switch {
			case item.MsgType == "text" && item.Text != nil:
				texts = append(texts, item.Text.Content)
			case item.MsgType == "image" && item.Image != nil:
				message.Media = append(message.Media, Media{Kind: MediaKindImage, URL: item.Image.URL, AESKey: item.Image.AESKey})
			}
		}
		message.Text = strings.Join(texts, "\n")
		return message
	}
	return unsupported
}

package telegram

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support"
)

const (
	// textLimit 是入站文本消息的最大字符数。
	textLimit = 4096
	// maxCursorUnixSecond 是消息时间允许的最大 Unix 秒数，换算为纳秒不溢出。
	maxCursorUnixSecond = int64(9223372036)
	// mediaFileIDLimit 是媒体文件编号的最大字节数。
	mediaFileIDLimit = 512
	// mediaFileNameLimit 是媒体文件名的最大字符数，超出时使用默认文件名。
	mediaFileNameLimit = 255
)

// ErrInvalidUpdate 表示回调请求体不是合法的 Telegram Update。
var ErrInvalidUpdate = errors.New("invalid telegram update")

// Update 定义解析后的 Telegram Update：MyChatMember 表示机器人成员状态变化，Message 为当前支持的私聊消息，IgnoredReason 记录被按范围忽略的消息原因。
type Update struct {
	ID            int64
	MyChatMember  bool
	Message       *InboundMessage
	IgnoredReason string
}

// ParseUpdate 解析单个 Telegram Update，拒绝缺少编号、多段 JSON、字段类型错误或同时携带多种更新的请求体，并归一化私聊消息。
func ParseUpdate(body []byte) (Update, error) {
	raw := struct {
		UpdateID     *int64          `json:"update_id"`
		MyChatMember json.RawMessage `json:"my_chat_member"`
		Message      json.RawMessage `json:"message"`
	}{}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&raw); err != nil || raw.UpdateID == nil {
		return Update{}, ErrInvalidUpdate
	}
	// 拒绝一个请求体中的多段 JSON。
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Update{}, ErrInvalidUpdate
	}
	myChatMember, validMember := objectPresent(raw.MyChatMember)
	messagePresent, validMessage := objectPresent(raw.Message)
	if !validMember || !validMessage || (myChatMember && messagePresent) {
		return Update{}, ErrInvalidUpdate
	}
	update := Update{ID: *raw.UpdateID, MyChatMember: myChatMember}
	if messagePresent {
		parsed := updateMessage{}
		if err := json.Unmarshal(raw.Message, &parsed); err != nil {
			return Update{}, ErrInvalidUpdate
		}
		update.Message, update.IgnoredReason = normalizeMessage(parsed)
	}
	return update, nil
}

// InboundReply 保存 Telegram 引用消息的编号与一层快照。
type InboundReply struct {
	MessageID   int64
	Body        string
	SenderName  string
	SenderIsBot bool
}

// InboundMedia 定义随 Telegram 消息送达、内容待取回的单个媒体文件。
type InboundMedia struct {
	FileID      string
	UniqueID    string
	FileName    string
	ContentType string
	ByteSize    int64
	Width       int
	Height      int
}

// InboundMessage 定义已归一化的 Telegram 私聊消息，Media 非空时 Body 为媒体说明；Unsupported 为真时消息内容不受支持，Body 与 Media 为空。
type InboundMessage struct {
	Reply        *InboundReply
	Media        *InboundMedia
	ChatID       int64
	MessageID    int64
	SenderID     int64
	DisplayName  string
	Body         string
	OriginatedAt time.Time
	Unsupported  bool
}

// updateFile 定义 Telegram 各类媒体共有的文件引用与元数据。
type updateFile struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileName     string `json:"file_name"`
	MimeType     string `json:"mime_type"`
	FileSize     int64  `json:"file_size"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
}

// updateMessage 定义 Update 中 message 字段的原始结构。
type updateMessage struct {
	ReplyTo   *updateMessage `json:"reply_to_message"`
	Photo     []updateFile   `json:"photo"`
	Document  *updateFile    `json:"document"`
	Voice     *updateFile    `json:"voice"`
	Video     *updateFile    `json:"video"`
	VideoNote *updateFile    `json:"video_note"`
	Audio     *updateFile    `json:"audio"`
	Animation *updateFile    `json:"animation"`
	Caption   string         `json:"caption"`
	MessageID int64          `json:"message_id"`
	Date      int64          `json:"date"`
	Text      *string        `json:"text"`
	Chat      struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
	From *struct {
		ID        int64  `json:"id"`
		IsBot     bool   `json:"is_bot"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
	} `json:"from"`
}

// objectPresent 校验可选 Telegram Update 字段是否为对象。
func objectPresent(value json.RawMessage) (bool, bool) {
	payload := bytes.TrimSpace(value)
	if len(payload) == 0 || string(payload) == "null" {
		return false, true
	}
	return true, payload[0] == '{'
}

// normalizeMessage 归一化私聊消息，内容不受支持的消息标记为 Unsupported。
func normalizeMessage(message updateMessage) (*InboundMessage, string) {
	if message.Chat.Type != "private" {
		return nil, "non_private"
	}
	if message.From == nil || message.From.IsBot || message.From.ID <= 0 || message.Chat.ID <= 0 || message.Chat.ID != message.From.ID || message.MessageID <= 0 || message.Date <= 0 || message.Date > maxCursorUnixSecond {
		return nil, "invalid_private_message"
	}
	displayName := strings.TrimSpace(strings.Join([]string{message.From.FirstName, message.From.LastName}, " "))
	if displayName == "" {
		return nil, "missing_sender_name"
	}
	inbound := &InboundMessage{
		ChatID: message.Chat.ID, MessageID: message.MessageID, SenderID: message.From.ID, DisplayName: displayName,
		OriginatedAt: time.Unix(message.Date, 0).UTC(),
	}
	var body string
	var media *InboundMedia
	if message.Text != nil {
		body = strings.TrimSpace(*message.Text)
		if body == "" || !utf8.ValidString(body) || utf8.RuneCountInString(body) > textLimit {
			return nil, "invalid_text"
		}
	} else {
		var ignoredReason string
		media, ignoredReason = normalizeMedia(message)
		if ignoredReason == "unsupported_content" {
			inbound.Unsupported = true
			return inbound, ""
		}
		if ignoredReason != "" {
			return nil, ignoredReason
		}
		body = strings.TrimSpace(message.Caption)
		if !utf8.ValidString(body) || utf8.RuneCountInString(body) > domain.ChannelCapabilitiesOf(domain.ChannelTypeTelegram).CaptionLimit {
			return nil, "invalid_caption"
		}
	}
	var reply *InboundReply
	// 原消息可由机器人发送，只读取同一聊天中的一层引用。
	if original := message.ReplyTo; original != nil && original.Chat.ID == message.Chat.ID && original.MessageID > 0 && original.MessageID != message.MessageID {
		reply = &InboundReply{MessageID: original.MessageID, Body: support.DerefOr(original.Text, original.Caption)}
		if original.From != nil {
			reply.SenderName = strings.TrimSpace(strings.Join([]string{original.From.FirstName, original.From.LastName}, " "))
			reply.SenderIsBot = original.From.IsBot
		}
	}
	inbound.Reply, inbound.Media, inbound.Body = reply, media, body
	return inbound, ""
}

// normalizeMedia 按动画、文件、照片、语音、视频、视频留言、音乐的顺序取出消息携带的单个媒体，缺少文件名或类型时按种类补默认值。
func normalizeMedia(message updateMessage) (*InboundMedia, string) {
	var file *updateFile
	var defaultName, defaultType string
	switch {
	case message.Animation != nil:
		// 动画消息同时携带 document 字段，按动画处理。
		file, defaultName, defaultType = message.Animation, "animation.mp4", "video/mp4"
	case message.Document != nil:
		file, defaultName, defaultType = message.Document, "document", "application/octet-stream"
	case len(message.Photo) > 0:
		// 照片取像素面积最大的档位。
		for index := range message.Photo {
			size := &message.Photo[index]
			if size.Width <= 0 || size.Height <= 0 {
				continue
			}
			if file == nil || size.Width*size.Height > file.Width*file.Height {
				file = size
			}
		}
		defaultName, defaultType = "photo.jpg", "image/jpeg"
	case message.Voice != nil:
		file, defaultName, defaultType = message.Voice, "voice.ogg", "audio/ogg"
	case message.Video != nil:
		file, defaultName, defaultType = message.Video, "video.mp4", "video/mp4"
	case message.VideoNote != nil:
		file, defaultName, defaultType = message.VideoNote, "video-note.mp4", "video/mp4"
	case message.Audio != nil:
		file, defaultName, defaultType = message.Audio, "audio.mp3", "audio/mpeg"
	default:
		return nil, "unsupported_content"
	}
	if file == nil || file.FileID == "" || len(file.FileID) > mediaFileIDLimit || file.FileUniqueID == "" || file.FileSize < 0 || file.Width < 0 || file.Height < 0 {
		return nil, "invalid_media"
	}
	media := &InboundMedia{
		FileID: file.FileID, UniqueID: file.FileUniqueID, ByteSize: file.FileSize,
		FileName: strings.TrimSpace(file.FileName), ContentType: strings.TrimSpace(file.MimeType),
	}
	if media.FileName == "" || !utf8.ValidString(media.FileName) || utf8.RuneCountInString(media.FileName) > mediaFileNameLimit {
		media.FileName = defaultName
	}
	if media.ContentType == "" {
		media.ContentType = defaultType
	}
	// 只有照片按图片尺寸内联展示，视频和动画的尺寸不作为图片宽高。
	if len(message.Photo) > 0 && message.Animation == nil && message.Document == nil {
		media.Width, media.Height = file.Width, file.Height
	}
	return media, ""
}

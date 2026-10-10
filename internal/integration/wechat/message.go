package wechat

import (
	"encoding/xml"
	"fmt"
)

// 公众号推送到消息接收地址的消息类型。
const (
	MessageTypeText       = "text"
	MessageTypeImage      = "image"
	MessageTypeVoice      = "voice"
	MessageTypeVideo      = "video"
	MessageTypeShortVideo = "shortvideo"
	MessageTypeLocation   = "location"
	MessageTypeLink       = "link"
	MessageTypeEvent      = "event"
)

// Message 定义公众号推送到消息接收地址的用户消息与事件。
type Message struct {
	ToUserName   string `xml:"ToUserName"`
	FromUserName string `xml:"FromUserName"`
	CreateTime   int64  `xml:"CreateTime"`
	MsgType      string `xml:"MsgType"`
	// MsgID 是用户消息编号，事件为空。
	MsgID   string `xml:"MsgId"`
	Content string `xml:"Content"`
	// MediaID 是图片、语音与视频消息的临时素材编号。
	MediaID string `xml:"MediaId"`
	// Format 是语音消息的音频格式。
	Format      string `xml:"Format"`
	LocationX   string `xml:"Location_X"`
	LocationY   string `xml:"Location_Y"`
	Label       string `xml:"Label"`
	Title       string `xml:"Title"`
	Description string `xml:"Description"`
	URL         string `xml:"Url"`
	Event       string `xml:"Event"`
	// EventKey 是菜单事件的菜单键或扫码事件的场景值。
	EventKey string `xml:"EventKey"`
}

// 公众号推送的用户互动事件。
const (
	EventSubscribe       = "subscribe"
	EventScan            = "SCAN"
	EventClick           = "CLICK"
	EventScanCodePush    = "scancode_push"
	EventScanCodeWaitMsg = "scancode_waitmsg"
)

// ParseMessage 解析明文或解密后的消息 XML。
func ParseMessage(plain []byte) (Message, error) {
	var message Message
	if err := xml.Unmarshal(plain, &message); err != nil {
		return Message{}, fmt.Errorf("parse wechat message: %w", err)
	}
	if message.FromUserName == "" || message.MsgType == "" {
		return Message{}, fmt.Errorf("wechat message missing sender or type")
	}
	return message, nil
}

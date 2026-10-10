package wechat

import (
	"crypto/subtle"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// PushTimestampTolerance 是推送签名时间戳与服务器时间允许的最大偏差。
const PushTimestampTolerance = 5 * time.Minute

// ErrTimestampExpired 表示推送签名时间戳缺失或超出允许偏差。
var ErrTimestampExpired = errors.New("wechat push timestamp expired")

// 开放平台授权事件接收地址收到的通知类型。
const (
	InfoTypeComponentVerifyTicket = "component_verify_ticket"
	InfoTypeAuthorized            = "authorized"
	InfoTypeUpdateAuthorized      = "updateauthorized"
	InfoTypeUnauthorized          = "unauthorized"
)

// PushQuery 定义推送的签名查询参数：明文推送与服务器地址验证使用 Signature，加密推送使用 MsgSignature。
type PushQuery struct {
	Timestamp    string
	Nonce        string
	Signature    string
	MsgSignature string
}

// envelope 定义加密推送的外层 XML。
type envelope struct {
	Encrypt string `xml:"Encrypt"`
}

// Open 校验加密推送的时间戳与签名，解密并校验接收方后返回消息明文 XML。
func (c *Cipher) Open(query PushQuery, body []byte, now time.Time) ([]byte, error) {
	if err := checkTimestamp(query.Timestamp, now); err != nil {
		return nil, err
	}
	var outer envelope
	if err := xml.Unmarshal(body, &outer); err != nil || outer.Encrypt == "" {
		return nil, ErrCiphertextInvalid
	}
	if err := c.VerifyEncrypted(query.MsgSignature, query.Timestamp, query.Nonce, outer.Encrypt); err != nil {
		return nil, err
	}
	return c.Decrypt(outer.Encrypt)
}

// VerifySignature 校验明文推送或服务器地址验证请求的时间戳与 signature。
func VerifySignature(token string, query PushQuery, now time.Time) error {
	if err := checkTimestamp(query.Timestamp, now); err != nil {
		return err
	}
	expected := Signature(token, query.Timestamp, query.Nonce)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(query.Signature)) != 1 {
		return ErrSignatureInvalid
	}
	return nil
}

// checkTimestamp 校验推送签名时间戳与 now 的偏差在允许范围内。
func checkTimestamp(timestamp string, now time.Time) error {
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrTimestampExpired
	}
	if skew := now.Sub(time.Unix(seconds, 0)); skew > PushTimestampTolerance || skew < -PushTimestampTolerance {
		return ErrTimestampExpired
	}
	return nil
}

// ComponentEvent 定义开放平台推送到授权事件接收地址的通知。
type ComponentEvent struct {
	AppID                        string `xml:"AppId"`
	CreateTime                   int64  `xml:"CreateTime"`
	InfoType                     string `xml:"InfoType"`
	ComponentVerifyTicket        string `xml:"ComponentVerifyTicket"`
	AuthorizerAppID              string `xml:"AuthorizerAppid"`
	AuthorizationCode            string `xml:"AuthorizationCode"`
	AuthorizationCodeExpiredTime int64  `xml:"AuthorizationCodeExpiredTime"`
	PreAuthCode                  string `xml:"PreAuthCode"`
}

// ParseComponentEvent 解析解密后的授权事件通知。
func ParseComponentEvent(plain []byte) (ComponentEvent, error) {
	var event ComponentEvent
	if err := xml.Unmarshal(plain, &event); err != nil {
		return ComponentEvent{}, fmt.Errorf("parse component event: %w", err)
	}
	if event.InfoType == "" {
		return ComponentEvent{}, fmt.Errorf("component event missing info type")
	}
	return event, nil
}

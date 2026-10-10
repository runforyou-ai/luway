package wechat

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// releaseTestAccounts 是微信全网发布检测使用的专用测试公众号，键为 AppID，值为原始 ID。
var releaseTestAccounts = map[string]string{
	"wx570bc396a51b8ff8": "gh_3c884a361561",
	"wx9252c5e0bb1836fc": "gh_c0f28a78b318",
	"wx8e1097c5bc82cde9": "gh_3f222ed8d140",
	"wx14550af28c71a144": "gh_26128078e9ab",
	"wxa35b9c23cfe664eb": "gh_2b3713f184a6",
}

// 全网发布检测的消息内容。
const (
	// ReleaseTestText 是文本消息检测发送的内容，被动回复 ReleaseTestText + ReleaseTestTextReplySuffix。
	ReleaseTestText            = "TESTCOMPONENT_MSG_TYPE_TEXT"
	ReleaseTestTextReplySuffix = "_callback"
	// ReleaseTestEventReplySuffix 是事件检测被动回复在事件类型之后附加的内容。
	ReleaseTestEventReplySuffix = "from_callback"
	// ReleaseTestAuthCodePrefix 是客服消息检测内容的前缀，其后是授权码，经客服消息接口回复授权码 + ReleaseTestAPIReplySuffix。
	ReleaseTestAuthCodePrefix = "QUERY_AUTH_CODE:"
	ReleaseTestAPIReplySuffix = "_from_api"
)

// IsReleaseTestAccount 判断 AppID 与原始 ID 属于同一个全网发布检测专用测试公众号。
func IsReleaseTestAccount(appID, userName string) bool {
	expected, ok := releaseTestAccounts[appID]
	return ok && expected == userName
}

// ReleaseTestAuthCode 返回客服消息检测内容中的授权码，内容不是客服消息检测时返回 false。
func ReleaseTestAuthCode(content string) (string, bool) {
	code, ok := strings.CutPrefix(content, ReleaseTestAuthCodePrefix)
	return code, ok && code != ""
}

// TextReply 返回公众号 from 被动回复用户 to 的文本消息明文 XML。
func TextReply(to, from string, createTime int64, content string) []byte {
	var body strings.Builder
	body.WriteString("<xml><ToUserName>")
	_ = xml.EscapeText(&body, []byte(to))
	body.WriteString("</ToUserName><FromUserName>")
	_ = xml.EscapeText(&body, []byte(from))
	body.WriteString("</FromUserName><CreateTime>" + strconv.FormatInt(createTime, 10) + "</CreateTime><MsgType>text</MsgType><Content>")
	_ = xml.EscapeText(&body, []byte(content))
	body.WriteString("</Content></xml>")
	return []byte(body.String())
}

// Seal 按微信规则加密被动回复的明文 XML 并签名，返回响应正文。
func (c *Cipher) Seal(message []byte, timestamp, nonce string) ([]byte, error) {
	encrypted, err := c.Encrypt(message)
	if err != nil {
		return nil, err
	}
	reply := struct {
		XMLName      xml.Name `xml:"xml"`
		Encrypt      string   `xml:"Encrypt"`
		MsgSignature string   `xml:"MsgSignature"`
		TimeStamp    string   `xml:"TimeStamp"`
		Nonce        string   `xml:"Nonce"`
	}{Encrypt: encrypted, MsgSignature: Signature(c.token, timestamp, nonce, encrypted), TimeStamp: timestamp, Nonce: nonce}
	body, err := xml.Marshal(reply)
	if err != nil {
		return nil, fmt.Errorf("encode wechat reply: %w", err)
	}
	return body, nil
}

//go:build server

package mail

import (
	"bytes"
	"html/template"
)

// noticeTemplate 是通知邮件的 HTML 正文。
var noticeTemplate = template.Must(template.New("notice").Parse(`<!doctype html>
<html lang="{{.Lang}}"><body style="margin:0;padding:24px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;color:#18181b;">
<p style="font-size:16px;font-weight:600;margin:0 0 12px;">{{.Heading}}</p>
<p style="font-size:14px;line-height:22px;margin:0 0 20px;">{{.Body}}</p>
<p style="margin:0 0 20px;"><a href="{{.Link}}" style="display:inline-block;padding:8px 16px;border-radius:6px;background:#2563eb;color:#ffffff;text-decoration:none;font-size:14px;">{{.Action}}</a></p>
<p style="font-size:12px;line-height:18px;color:#71717a;margin:0;">{{.Note}}</p>
</body></html>`))

// Notice 定义一封带操作按钮的通知邮件：标题同时作为邮件主题，Note 是按钮下方的补充说明。
type Notice struct {
	FromName string
	To       string
	Lang     string
	Heading  string
	Body     string
	Action   string
	Link     string
	Note     string
}

// Message 生成通知邮件的纯文本与 HTML 正文。
func (n Notice) Message() Message {
	var html bytes.Buffer
	_ = noticeTemplate.Execute(&html, n)
	text := n.Heading + "\n\n" + n.Body + "\n\n" + n.Action + ": " + n.Link + "\n\n" + n.Note + "\n"
	return Message{FromName: n.FromName, To: n.To, Subject: n.Heading, Text: text, HTML: html.String()}
}

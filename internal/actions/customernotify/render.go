//go:build server

package customernotify

import (
	"fmt"
	htmltemplate "html/template"
	"strings"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/pkg/mail"
)

// notificationContent 是一封客服回复通知邮件的内容。
type notificationContent struct {
	Locale    domain.CustomerLocale
	Workspace string
	Recipient string
	Replies   []notificationReply
	// ResumeURL 是匿名访客回到原会话的链接，签名身份访客为空。
	ResumeURL string
}

// notificationReply 是邮件中的一条客服回复。
type notificationReply struct {
	SenderName     string
	Body           string
	AttachmentName *string
}

// notificationView 是 HTML 邮件模板的数据。
type notificationView struct {
	Lang      string
	Heading   string
	Replies   []notificationViewReply
	ResumeURL string
	Continue  string
	Hint      string
	Footer    string
}

// notificationViewReply 是 HTML 邮件模板中的一条回复。
type notificationViewReply struct {
	SenderName string
	Body       string
	Attachment string
}

// notificationTemplate 是客服回复通知邮件的 HTML 模板。
var notificationTemplate = htmltemplate.Must(htmltemplate.New("notification").Parse(`<!doctype html>
<html lang="{{.Lang}}"><body style="margin:0;padding:24px;background:#f5f5f4;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Helvetica,Arial,'Nirmala UI',sans-serif;color:#1c1917;">
<div style="max-width:560px;margin:0 auto;background:#ffffff;border-radius:12px;padding:28px;">
<h1 style="margin:0 0 20px;font-size:18px;font-weight:600;">{{.Heading}}</h1>
{{range .Replies}}<div style="margin:0 0 16px;">
<div style="font-size:13px;color:#78716c;margin:0 0 4px;">{{.SenderName}}</div>
{{if .Body}}<div style="font-size:15px;line-height:1.6;white-space:pre-wrap;">{{.Body}}</div>{{end}}
{{if .Attachment}}<div style="font-size:14px;color:#57534e;">{{.Attachment}}</div>{{end}}
</div>{{end}}
{{if .ResumeURL}}<a href="{{.ResumeURL}}" style="display:inline-block;margin:8px 0 0;padding:10px 18px;border-radius:8px;background:#1c1917;color:#ffffff;text-decoration:none;font-size:14px;font-weight:600;">{{.Continue}}</a>{{else}}<p style="margin:8px 0 0;font-size:14px;color:#57534e;">{{.Hint}}</p>{{end}}
<p style="margin:28px 0 0;font-size:12px;color:#a8a29e;">{{.Footer}}</p>
</div></body></html>`))

// renderNotification 按客户语言生成纯文本与 HTML 两部分的通知邮件。
func renderNotification(content notificationContent) (mail.Message, error) {
	workspace := map[string]any{"Workspace": content.Workspace}
	view := notificationView{
		Lang:      string(content.Locale),
		Heading:   i18n.LocalizeCustomerTemplate(content.Locale, i18n.CustomerEmailSubject, workspace),
		ResumeURL: content.ResumeURL,
		Continue:  i18n.LocalizeCustomerTemplate(content.Locale, i18n.CustomerEmailContinue, nil),
		Hint:      i18n.LocalizeCustomerTemplate(content.Locale, i18n.CustomerEmailSignInHint, workspace),
		Footer:    i18n.LocalizeCustomerTemplate(content.Locale, i18n.CustomerEmailFooter, nil),
	}
	var text strings.Builder
	text.WriteString(view.Heading + "\n\n")
	for _, reply := range content.Replies {
		item := notificationViewReply{SenderName: reply.SenderName, Body: reply.Body}
		if reply.AttachmentName != nil {
			item.Attachment = i18n.LocalizeCustomerTemplate(content.Locale, i18n.CustomerEmailAttachment, map[string]any{"Name": *reply.AttachmentName})
		}
		view.Replies = append(view.Replies, item)
		text.WriteString(item.SenderName + "\n")
		for _, line := range []string{item.Body, item.Attachment} {
			if line != "" {
				text.WriteString(line + "\n")
			}
		}
		text.WriteString("\n")
	}
	if view.ResumeURL != "" {
		text.WriteString(view.Continue + ": " + view.ResumeURL + "\n\n")
	} else {
		text.WriteString(view.Hint + "\n\n")
	}
	text.WriteString(view.Footer + "\n")
	var html strings.Builder
	if err := notificationTemplate.Execute(&html, view); err != nil {
		return mail.Message{}, fmt.Errorf("render customer notification: %w", err)
	}
	return mail.Message{
		FromName: content.Workspace, To: content.Recipient, Subject: view.Heading,
		Text: text.String(), HTML: html.String(),
	}, nil
}

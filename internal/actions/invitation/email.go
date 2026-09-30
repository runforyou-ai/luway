//go:build server

package invitation

import (
	"bytes"
	"html/template"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/pkg/mail"
)

// invitationEmailTemplate 是邀请邮件的 HTML 正文。
var invitationEmailTemplate = template.Must(template.New("invitation").Parse(`<!doctype html>
<html lang="{{.Lang}}"><body style="margin:0;padding:24px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;color:#18181b;">
<p style="font-size:16px;font-weight:600;margin:0 0 12px;">{{.Heading}}</p>
<p style="font-size:14px;line-height:22px;margin:0 0 20px;">{{.Body}}</p>
<p style="margin:0 0 20px;"><a href="{{.Link}}" style="display:inline-block;padding:8px 16px;border-radius:6px;background:#2563eb;color:#ffffff;text-decoration:none;font-size:14px;">{{.Action}}</a></p>
<p style="font-size:12px;line-height:18px;color:#71717a;margin:0;">{{.Expiry}}</p>
</body></html>`))

// renderInvitationEmail 按发起人的界面语言生成邀请邮件。
func renderInvitationEmail(locale domain.Locale, workspaceName, inviterName, to, link string) mail.Message {
	data := map[string]any{"Workspace": workspaceName, "Inviter": inviterName}
	language := string(locale)
	view := map[string]string{
		"Lang":    language,
		"Heading": i18n.LocalizeTemplate(language, i18n.InvitationEmailSubject, data),
		"Body":    i18n.LocalizeTemplate(language, i18n.InvitationEmailBody, data),
		"Action":  i18n.LocalizeTemplate(language, i18n.InvitationEmailAction, data),
		"Expiry":  i18n.LocalizeTemplate(language, i18n.InvitationEmailExpiry, data),
		"Link":    link,
	}
	var html bytes.Buffer
	_ = invitationEmailTemplate.Execute(&html, view)
	text := view["Heading"] + "\n\n" + view["Body"] + "\n\n" + view["Action"] + ": " + link + "\n\n" + view["Expiry"] + "\n"
	return mail.Message{FromName: workspaceName, To: to, Subject: view["Heading"], Text: text, HTML: html.String()}
}

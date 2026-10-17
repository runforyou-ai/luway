//go:build server

package deployment

import (
	"context"
	"html"
	"net"
	"regexp"
	"strings"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/pkg/mail"
	"github.com/runforyou-ai/support/str"
)

const ValidationEmailSettingInvalid common.FieldCode = "PLATFORM_EMAIL_SETTING_INVALID"

var emailRegionPattern = regexp.MustCompile(`^[a-z]{2,8}(?:-[a-z]+)+-[0-9]+$`)
var emailDomainLabel = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?$`)

// normalizeEmail 在平台行锁内校验并保留同服务商、同凭据身份的密钥。
func normalizeEmail(input, previous mail.Config) (mail.Config, map[string]common.FieldCode) {
	c := input
	c.FromAddress, c.FromName = strings.TrimSpace(c.FromAddress), strings.TrimSpace(c.FromName)
	fields := map[string]common.FieldCode{}
	invalid := func(field string) { fields[field] = ValidationEmailSettingInvalid }
	if (c.Enabled || c.FromAddress != "") && !str.IsEmail(c.FromAddress) {
		fields["fromAddress"] = ValidationSMTPFromAddressInvalid
	}
	if len([]rune(c.FromName)) > 128 || strings.ContainsAny(c.FromName, "\r\n") {
		invalid("fromName")
	}
	selected := map[string]bool{"smtp": c.SMTP != nil, "ses": c.SES != nil, "mailgun": c.Mailgun != nil, "postmark": c.Postmark != nil, "resend": c.Resend != nil}
	if !selected[c.Provider] {
		invalid("provider")
	}
	for provider, present := range selected {
		if present && provider != c.Provider {
			invalid(provider)
		}
	}
	if len(fields) > 0 {
		return c, fields
	}
	same := previous.Provider == c.Provider
	switch c.Provider {
	case "smtp":
		options := *c.SMTP
		c.SMTP = &options
		options.Host, options.Username, options.Security = strings.TrimSpace(options.Host), strings.TrimSpace(options.Username), strings.ToLower(strings.TrimSpace(options.Security))
		if (c.Enabled || options.Host != "") && net.ParseIP(options.Host) == nil && !validEmailDomain(options.Host) {
			invalid("smtp.host")
		}
		if options.Port < 1 || options.Port > 65535 {
			fields["smtp.port"] = ValidationSMTPPortInvalid
		}
		if options.Security != "starttls" && options.Security != "tls" && options.Security != "none" {
			fields["smtp.security"] = ValidationSMTPSecurityInvalid
		}
		if options.Username == "" {
			if options.Password != "" {
				fields["smtp.password"] = ValidationSMTPCredentialsIncomplete
			}
			options.Password = ""
		} else if options.Password == "" && same && previous.SMTP != nil && previous.SMTP.Host == options.Host && previous.SMTP.Username == options.Username {
			options.Password = previous.SMTP.Password
		}
		if c.Enabled && options.Username != "" && options.Password == "" {
			fields["smtp.password"] = ValidationSMTPCredentialsIncomplete
		}
		if len(options.Username) > 320 || strings.ContainsAny(options.Username, "\r\n") {
			invalid("smtp.username")
		}
		if len(options.Password) > 4096 || strings.ContainsAny(options.Password, "\r\n") {
			invalid("smtp.password")
		}
	case "ses":
		options := *c.SES
		c.SES = &options
		options.Region, options.AccessKeyID, options.SecretAccessKey = strings.TrimSpace(options.Region), strings.TrimSpace(options.AccessKeyID), strings.TrimSpace(options.SecretAccessKey)
		if (c.Enabled || options.Region != "") && !emailRegionPattern.MatchString(options.Region) {
			invalid("ses.region")
		}
		if options.SecretAccessKey == "" && same && previous.SES != nil && previous.SES.AccessKeyID == options.AccessKeyID {
			options.SecretAccessKey = previous.SES.SecretAccessKey
		}
		if (c.Enabled && options.AccessKeyID == "") || len(options.AccessKeyID) > 128 || strings.ContainsAny(options.AccessKeyID, "\r\n") {
			invalid("ses.accessKeyID")
		}
		validateEmailSecret(options.SecretAccessKey, c.Enabled, "ses.secretAccessKey", fields)
	case "mailgun":
		options := *c.Mailgun
		c.Mailgun = &options
		options.Domain, options.Region, options.APIKey = strings.TrimSpace(options.Domain), strings.TrimSpace(options.Region), strings.TrimSpace(options.APIKey)
		if (c.Enabled || options.Domain != "") && !validEmailDomain(options.Domain) {
			invalid("mailgun.domain")
		}
		if options.Region != "us" && options.Region != "eu" {
			invalid("mailgun.region")
		}
		if options.APIKey == "" && same && previous.Mailgun != nil {
			options.APIKey = previous.Mailgun.APIKey
		}
		validateEmailSecret(options.APIKey, c.Enabled, "mailgun.apiKey", fields)
	case "postmark":
		options := *c.Postmark
		c.Postmark = &options
		options.ServerToken, options.MessageStream = strings.TrimSpace(options.ServerToken), strings.TrimSpace(options.MessageStream)
		if options.ServerToken == "" && same && previous.Postmark != nil {
			options.ServerToken = previous.Postmark.ServerToken
		}
		validateEmailSecret(options.ServerToken, c.Enabled, "postmark.serverToken", fields)
		if len(options.MessageStream) > 128 || strings.ContainsAny(options.MessageStream, "\r\n") {
			invalid("postmark.messageStream")
		}
	case "resend":
		options := *c.Resend
		c.Resend = &options
		options.APIKey = strings.TrimSpace(options.APIKey)
		if options.APIKey == "" && same && previous.Resend != nil {
			options.APIKey = previous.Resend.APIKey
		}
		validateEmailSecret(options.APIKey, c.Enabled, "resend.apiKey", fields)
	}
	return c, fields
}

// validEmailDomain 校验 SMTP 主机或 Mailgun 域名的各级标签。
func validEmailDomain(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) > 63 || !emailDomainLabel.MatchString(label) {
			return false
		}
	}
	return true
}

// validateEmailSecret 校验启用时必填的服务商密钥。
func validateEmailSecret(value string, required bool, field string, fields map[string]common.FieldCode) {
	if (required && value == "") || len(value) > 4096 || strings.ContainsAny(value, "\r\n") {
		fields[field] = ValidationEmailSettingInvalid
	}
}

// TestEmail 使用已保存的配置发送本地化测试邮件。
func (a *UpdateDeploymentAction) TestEmail(ctx context.Context, to, locale string) error {
	settings, err := NewDeploymentSettingsQuery(a.db).Execute(ctx)
	if err != nil {
		return err
	}
	body, _ := i18n.Localize(locale, i18n.MailTestBody)
	subject, _ := i18n.Localize(locale, i18n.MailTestSubject)
	return mail.NewClient(settings.Email, nil).Send(ctx, mail.Message{
		To: to, Subject: subject, Text: body, HTML: "<p>" + html.EscapeString(body) + "</p>",
	})
}

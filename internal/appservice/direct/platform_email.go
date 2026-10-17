//go:build server

package direct

import (
	"context"
	"errors"

	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/mail"
)

// emailConfigFromInput 将当前服务商的保存参数转换为发送配置。
func emailConfigFromInput(input appservice.PlatformEmailInput) mail.Config {
	c := mail.Config{Enabled: input.Enabled, Provider: string(input.Provider), FromAddress: input.FromAddress, FromName: input.FromName}
	if input.SMTP != nil {
		c.SMTP = &mail.SMTPConfig{Host: input.SMTP.Host, Port: input.SMTP.Port, Security: input.SMTP.Security, Username: input.SMTP.Username, Password: input.SMTP.Password}
	}
	if input.SES != nil {
		c.SES = &mail.SESConfig{Region: input.SES.Region, AccessKeyID: input.SES.AccessKeyID, SecretAccessKey: input.SES.SecretAccessKey}
	}
	if input.Mailgun != nil {
		c.Mailgun = &mail.MailgunConfig{Domain: input.Mailgun.Domain, Region: input.Mailgun.Region, APIKey: input.Mailgun.APIKey}
	}
	if input.Postmark != nil {
		c.Postmark = &mail.PostmarkConfig{MessageStream: input.Postmark.MessageStream, ServerToken: input.Postmark.ServerToken}
	}
	if input.Resend != nil {
		c.Resend = &mail.ResendConfig{APIKey: input.Resend.APIKey}
	}
	return c
}

// emailSettingsFromConfig 只输出当前服务商的非密字段和密钥状态。
func emailSettingsFromConfig(c mail.Config) appservice.PlatformEmailSettings {
	result := appservice.PlatformEmailSettings{Enabled: c.Enabled, Provider: appservice.EmailProvider(c.Provider), FromAddress: c.FromAddress, FromName: c.FromName}
	if c.SMTP != nil {
		result.SMTP = &appservice.PlatformEmailSMTPSettings{Host: c.SMTP.Host, Port: c.SMTP.Port, Security: c.SMTP.Security, Username: c.SMTP.Username, PasswordSet: c.SMTP.Password != ""}
	}
	if c.SES != nil {
		result.SES = &appservice.PlatformEmailSESSettings{Region: c.SES.Region, AccessKeyID: c.SES.AccessKeyID, SecretAccessKeySet: c.SES.SecretAccessKey != ""}
	}
	if c.Mailgun != nil {
		result.Mailgun = &appservice.PlatformEmailMailgunSettings{Domain: c.Mailgun.Domain, Region: c.Mailgun.Region, APIKeySet: c.Mailgun.APIKey != ""}
	}
	if c.Postmark != nil {
		result.Postmark = &appservice.PlatformEmailPostmarkSettings{MessageStream: c.Postmark.MessageStream, ServerTokenSet: c.Postmark.ServerToken != ""}
	}
	if c.Resend != nil {
		result.Resend = &appservice.PlatformEmailResendSettings{APIKeySet: c.Resend.APIKey != ""}
	}
	return result
}

// TestPlatformEmail 测试已保存的邮件配置并转换安全的投递错误类别。
func (o *platformOps) TestPlatformEmail(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PlatformEmailTestInput) error {
	err := o.updateDeployment.TestEmail(ctx, input.To, string(meta.Locale))
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, mail.ErrDisabled):
		return appservice.InvalidError(meta, i18n.ErrorMailDisabled, nil)
	case errors.Is(err, mail.ErrAuthentication):
		return appservice.InvalidError(meta, i18n.ErrorMailAuthentication, nil)
	case errors.Is(err, mail.ErrRateLimited):
		return appservice.UnavailableError(meta, i18n.ErrorMailRateLimited, nil)
	case errors.Is(err, mail.ErrRejected):
		return appservice.InvalidError(meta, i18n.ErrorMailRejected, nil)
	case errors.Is(err, context.DeadlineExceeded):
		return appservice.UnavailableError(meta, i18n.ErrorMailTimeout, nil)
	case errors.Is(err, mail.ErrUnavailable):
		return appservice.UnavailableError(meta, i18n.ErrorMailTestFailed, nil)
	case errors.Is(err, mail.ErrConfiguration), errors.Is(err, mail.ErrMessage):
		return appservice.InvalidError(meta, i18n.FieldEmailSettingInvalid, nil)
	}
	return appservice.FailedError(meta, i18n.ErrorMailTestFailed, err)
}

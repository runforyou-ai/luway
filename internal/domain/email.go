package domain

// EmailProvider 定义系统邮件的接入方式。
type EmailProvider string

const (
	// EmailProviderSMTP 通过 SMTP 服务器发送邮件。
	EmailProviderSMTP EmailProvider = "smtp"
	// EmailProviderSES 通过 Amazon SES API 发送邮件。
	EmailProviderSES EmailProvider = "ses"
	// EmailProviderMailgun 通过 Mailgun API 发送邮件。
	EmailProviderMailgun EmailProvider = "mailgun"
	// EmailProviderPostmark 通过 Postmark API 发送邮件。
	EmailProviderPostmark EmailProvider = "postmark"
	// EmailProviderResend 通过 Resend API 发送邮件。
	EmailProviderResend EmailProvider = "resend"
)

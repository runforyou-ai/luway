//go:build server

package mail

// Config 定义一个服务商的邮件发送配置，只有 Provider 对应的参数非空。
type Config struct {
	Enabled     bool            `json:"enabled"`
	Provider    string          `json:"provider"`
	FromAddress string          `json:"fromAddress"`
	FromName    string          `json:"fromName"`
	SMTP        *SMTPConfig     `json:"smtp,omitempty"`
	SES         *SESConfig      `json:"ses,omitempty"`
	Mailgun     *MailgunConfig  `json:"mailgun,omitempty"`
	Postmark    *PostmarkConfig `json:"postmark,omitempty"`
	Resend      *ResendConfig   `json:"resend,omitempty"`
}

// SMTPConfig 定义 SMTP 连接和认证参数。
type SMTPConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Security string `json:"security"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// SESConfig 定义 Amazon SES API 区域和访问凭据。
type SESConfig struct {
	Region          string `json:"region"`
	AccessKeyID     string `json:"accessKeyID"`
	SecretAccessKey string `json:"secretAccessKey"`
}

// MailgunConfig 定义 Mailgun 发信域名、区域和密钥。
type MailgunConfig struct {
	Domain string `json:"domain"`
	Region string `json:"region"`
	APIKey string `json:"apiKey"`
}

// PostmarkConfig 定义 Postmark 服务器令牌与消息流。
type PostmarkConfig struct {
	ServerToken   string `json:"serverToken"`
	MessageStream string `json:"messageStream"`
}

// ResendConfig 定义 Resend API 密钥。
type ResendConfig struct {
	APIKey string `json:"apiKey"`
}

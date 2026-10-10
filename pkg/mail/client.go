//go:build server

// Package mail 通过 SMTP 服务发送邮件。
package mail

import (
	"context"
	"fmt"
	"time"

	gomail "github.com/wneessen/go-mail"
)

// sendTimeout 是一次 SMTP 连接与投递的超时时间。
const sendTimeout = 30 * time.Second

// Config 定义 SMTP 发信服务。
type Config struct {
	Host        string
	Port        int
	Username    string
	Password    string
	Security    string // starttls、tls 或 none。
	FromAddress string
}

// Message 定义一封同时带纯文本与 HTML 正文的邮件。
type Message struct {
	FromName string
	To       string
	Subject  string
	Text     string
	HTML     string
}

// Client 使用 SMTP 服务发送邮件。
type Client struct {
	config Config
}

// NewClient 创建 SMTP 发信客户端。
func NewClient(config Config) *Client {
	return &Client{config: config}
}

// Send 建立一次 SMTP 连接并投递邮件。
func (c *Client) Send(ctx context.Context, message Message) error {
	msg := gomail.NewMsg()
	if err := msg.FromFormat(message.FromName, c.config.FromAddress); err != nil {
		return fmt.Errorf("set mail sender: %w", err)
	}
	if err := msg.To(message.To); err != nil {
		return fmt.Errorf("set mail recipient: %w", err)
	}
	msg.Subject(message.Subject)
	msg.SetMessageID()
	msg.SetDate()
	msg.SetBodyString(gomail.TypeTextPlain, message.Text)
	msg.AddAlternativeString(gomail.TypeTextHTML, message.HTML)

	options := []gomail.Option{gomail.WithPort(c.config.Port), gomail.WithTimeout(sendTimeout)}
	switch c.config.Security {
	case "tls":
		options = append(options, gomail.WithSSL())
	case "none":
		options = append(options, gomail.WithTLSPolicy(gomail.NoTLS))
	default:
		options = append(options, gomail.WithTLSPolicy(gomail.TLSMandatory))
	}
	if c.config.Username != "" {
		options = append(options, gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover),
			gomail.WithUsername(c.config.Username), gomail.WithPassword(c.config.Password))
	}
	client, err := gomail.NewClient(c.config.Host, options...)
	if err != nil {
		return fmt.Errorf("create smtp client: %w", err)
	}
	if err := client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("send mail: %w", err)
	}
	return nil
}

//go:build server

// Package mail 通过 SMTP 或邮件服务商 API 投递邮件。
package mail

import (
	"context"
	"errors"
	"net"
	"net/http"
	netmail "net/mail"
	"net/textproto"
	"strings"
	"time"

	gomail "github.com/wneessen/go-mail"
)

// sendTimeout 是一次邮件投递的总时限。
const sendTimeout = 30 * time.Second

// 邮件投递错误只包含可记录的类别。
var (
	ErrDisabled       = errors.New("mail delivery is disabled")
	ErrConfiguration  = errors.New("invalid mail configuration")
	ErrMessage        = errors.New("invalid mail message")
	ErrAuthentication = errors.New("mail authentication failed")
	ErrRateLimited    = errors.New("mail provider rate limit exceeded")
	ErrRejected       = errors.New("mail provider rejected the message")
	ErrUnavailable    = errors.New("mail service unavailable")
)

// Message 定义一封同时带纯文本与 HTML 正文的邮件。
type Message struct {
	FromName string
	To       string
	Subject  string
	Text     string
	HTML     string
}

// Client 使用一份配置向当前服务商投递邮件。
type Client struct {
	config Config
	http   *http.Client
}

// NewClient 创建发信客户端，httpClient 为空时使用标准 HTTP 传输；API 请求禁止重定向。
func NewClient(config Config, httpClient *http.Client) *Client {
	client := http.Client{}
	if httpClient != nil {
		client = *httpClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{config: config, http: &client}
}

// Send 校验消息后向当前服务商投递，重试由调用方管理。
func (c *Client) Send(ctx context.Context, message Message) error {
	if !c.config.Enabled {
		return ErrDisabled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.config.FromName != "" {
		message.FromName = c.config.FromName
	}
	from, err := netmail.ParseAddress(c.config.FromAddress)
	if err != nil || from.Address != c.config.FromAddress || strings.ContainsAny(message.FromName+message.Subject, "\r\n") {
		return ErrMessage
	}
	to, err := netmail.ParseAddress(message.To)
	if err != nil || to.Address != message.To {
		return ErrMessage
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	switch c.config.Provider {
	case "smtp":
		if c.config.SMTP == nil {
			return ErrConfiguration
		}
		err = c.sendSMTP(ctx, message)
	case "ses":
		if c.config.SES == nil {
			return ErrConfiguration
		}
		err = c.sendSES(ctx, message)
	case "mailgun", "postmark", "resend":
		err = c.sendAPI(ctx, message)
	default:
		return ErrConfiguration
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var network net.Error
		if errors.As(err, &network) && network.Timeout() {
			return context.DeadlineExceeded
		}
	}
	return err
}

// sendSMTP 建立一次 SMTP 连接并提交双正文邮件。
func (c *Client) sendSMTP(ctx context.Context, message Message) error {
	config := c.config.SMTP
	msg := gomail.NewMsg()
	if err := msg.FromFormat(message.FromName, c.config.FromAddress); err != nil {
		return ErrMessage
	}
	if err := msg.To(message.To); err != nil {
		return ErrMessage
	}
	msg.Subject(message.Subject)
	msg.SetMessageID()
	msg.SetDate()
	msg.SetBodyString(gomail.TypeTextPlain, message.Text)
	msg.AddAlternativeString(gomail.TypeTextHTML, message.HTML)
	options := []gomail.Option{gomail.WithPort(config.Port), gomail.WithTimeout(sendTimeout)}
	switch config.Security {
	case "tls":
		options = append(options, gomail.WithSSL())
	case "none":
		options = append(options, gomail.WithTLSPolicy(gomail.NoTLS))
	case "starttls":
		options = append(options, gomail.WithTLSPolicy(gomail.TLSMandatory))
	default:
		return ErrConfiguration
	}
	if config.Username != "" {
		options = append(options, gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover), gomail.WithUsername(config.Username), gomail.WithPassword(config.Password))
	}
	client, err := gomail.NewClient(config.Host, options...)
	if err != nil {
		return ErrConfiguration
	}
	if err = client.DialAndSendWithContext(ctx, msg); err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return context.DeadlineExceeded
	}
	// SMTP 服务器的文本响应仅用于分类，返回值只包含安全类别。
	code := 0
	var protocol *textproto.Error
	var send *gomail.SendError
	if errors.As(err, &protocol) {
		code = protocol.Code
	}
	if errors.As(err, &send) {
		code = send.ErrorCode()
	}
	switch {
	case code == 530 || code == 534 || code == 535 || code == 538:
		return ErrAuthentication
	case code >= 500:
		return ErrRejected
	default:
		return ErrUnavailable
	}
}

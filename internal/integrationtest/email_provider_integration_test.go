//go:build server

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/servertest"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/runforyou-ai/luway/pkg/mail"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// emailFixture 持有一套独立部署的邮件配置测试资源。
type emailFixture struct {
	db      *bun.DB
	backend *direct.Backend
	state   *deploymentaction.DeploymentState
	meta    appservice.RequestMeta
}

// newEmailFixture 安装一套独立平台并返回管理员会话。
func newEmailFixture(t *testing.T) emailFixture {
	t.Helper()
	db := servertest.OpenEmptyDatabase(t, serverstorage.CoreMigrations())
	state := servertest.NewDeployment(t, db)
	backend := direct.New(db, direct.DeploymentConfig{Deployment: state, TaskMonitor: &fakeTaskMonitor{}}, nil, nil, testEnqueuer, nil, nil, nil)
	admin := servertest.InstallPlatform(t, backend, appservice.InstallPlatformInput{
		PublicURL: servertest.PublicURL, DisplayName: "Admin", Email: servertest.UniqueEmail("mail-settings"), Password: "password123", Locale: domain.LocaleEnglishUnitedStates, TimeZone: "UTC",
	})
	return emailFixture{db: db, backend: backend, state: state, meta: servertest.AdminMeta(admin.AdminToken)}
}

// emailProviderInput 返回五种接入方式各自完整的保存参数。
func emailProviderInput(provider domain.EmailProvider) appservice.PlatformEmailInput {
	input := appservice.PlatformEmailInput{Enabled: true, Provider: provider, FromAddress: "sender@example.test", FromName: "Support"}
	switch provider {
	case domain.EmailProviderSMTP:
		input.SMTP = &appservice.PlatformEmailSMTPInput{Host: "smtp.example.test", Port: 587, Security: "starttls", Username: "mailer", Password: "smtp-secret"}
	case domain.EmailProviderSES:
		input.SES = &appservice.PlatformEmailSESInput{Region: "us-east-1", AccessKeyID: "test-access-key", SecretAccessKey: "ses-secret"}
	case domain.EmailProviderMailgun:
		input.Mailgun = &appservice.PlatformEmailMailgunInput{Domain: "mg.example.test", Region: "eu", APIKey: "mailgun-secret"}
	case domain.EmailProviderPostmark:
		input.Postmark = &appservice.PlatformEmailPostmarkInput{ServerToken: "postmark-secret", MessageStream: "transactional"}
	case domain.EmailProviderResend:
		input.Resend = &appservice.PlatformEmailResendInput{APIKey: "resend-secret"}
	}
	return input
}

// TestEmailProviderSettings 验证五种配置、跨实例生效、只保存当前方式和读取脱敏。
func TestEmailProviderSettings(t *testing.T) {
	t.Parallel()
	f := newEmailFixture(t)
	ctx := context.Background()
	other := servertest.NewDeployment(t, f.db)
	for _, provider := range []domain.EmailProvider{domain.EmailProviderSMTP, domain.EmailProviderSES, domain.EmailProviderMailgun, domain.EmailProviderPostmark, domain.EmailProviderResend} {
		t.Run(string(provider), func(t *testing.T) {
			updated, err := f.backend.UpdatePlatformEmail(ctx, f.meta, emailProviderInput(provider))
			require.NoError(t, err)
			require.True(t, updated.Email.Enabled)
			require.Equal(t, provider, updated.Email.Provider)
			encoded, err := json.Marshal(updated)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "-secret")
			fetched, err := f.backend.GetPlatformDeployment(ctx, f.meta)
			require.NoError(t, err)
			require.Equal(t, updated.Email, fetched.Email)
			require.True(t, f.state.Mailer().Enabled())
			require.NoError(t, other.Reload(ctx))
			require.Equal(t, string(provider), other.Current().Settings.Email.Provider)
			var stored string
			require.NoError(t, f.db.NewSelect().Table("platforms").ColumnExpr("email_config::text").Scan(ctx, &stored))
			var data map[string]any
			require.NoError(t, json.Unmarshal([]byte(stored), &data))
			require.Contains(t, data, string(provider))
			require.Len(t, data, 5)
			diagnostics, err := f.backend.GetPlatformDiagnostics(ctx, f.meta)
			require.NoError(t, err)
			encoded, err = json.Marshal(diagnostics)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "-secret")
			require.NotContains(t, string(encoded), "test-access-key")
		})
	}
	// 其他服务商的残留字段和未知方式均不保存。
	invalid := emailProviderInput(domain.EmailProviderResend)
	invalid.SMTP = &appservice.PlatformEmailSMTPInput{}
	_, err := f.backend.UpdatePlatformEmail(ctx, f.meta, invalid)
	servertest.RequireFieldError(t, err, "smtp", i18n.FieldEmailSettingInvalid)
	invalid = appservice.PlatformEmailInput{Provider: "sendmail"}
	_, err = f.backend.UpdatePlatformEmail(ctx, f.meta, invalid)
	servertest.RequireFieldError(t, err, "provider", i18n.FieldEmailSettingInvalid)
	disabled := emailProviderInput(domain.EmailProviderResend)
	disabled.Enabled = false
	disabled.FromAddress = ""
	disabled.Resend.APIKey = ""
	_, err = f.backend.UpdatePlatformEmail(ctx, f.meta, disabled)
	require.NoError(t, err)
	require.False(t, f.state.Mailer().Enabled())
	err = f.backend.TestPlatformEmail(ctx, f.meta, appservice.PlatformEmailTestInput{To: "recipient@example.test"})
	servertest.RequireLocalizedError(t, err, i18n.ErrorMailDisabled)
	var apiError *appservice.Error
	require.ErrorAs(t, err, &apiError)
	require.Equal(t, http.StatusBadRequest, apiError.HTTPStatus())
	err = f.backend.TestPlatformEmail(ctx, f.meta, appservice.PlatformEmailTestInput{To: "bad"})
	servertest.RequireFieldError(t, err, "to", i18n.FieldEmailInvalid)
}

// TestEmailSecretUpdates 验证密钥保留、替换、身份变更与切换方式的边界。
func TestEmailSecretUpdates(t *testing.T) {
	t.Parallel()
	f := newEmailFixture(t)
	ctx := context.Background()
	smtp := emailProviderInput(domain.EmailProviderSMTP)
	_, err := f.backend.UpdatePlatformEmail(ctx, f.meta, smtp)
	require.NoError(t, err)
	smtp.SMTP.Password = "invalid\r\npassword"
	_, err = f.backend.UpdatePlatformEmail(ctx, f.meta, smtp)
	servertest.RequireFieldError(t, err, "smtp.password", i18n.FieldEmailSettingInvalid)
	smtp.SMTP.Password = ""
	_, err = f.backend.UpdatePlatformEmail(ctx, f.meta, smtp)
	require.NoError(t, err)
	require.Equal(t, "smtp-secret", f.state.Current().Settings.Email.SMTP.Password)
	smtp.SMTP.Username = "another"
	_, err = f.backend.UpdatePlatformEmail(ctx, f.meta, smtp)
	servertest.RequireFieldError(t, err, "smtp.password", i18n.FieldSMTPCredentialsIncomplete)
	smtp.SMTP.Username = "mailer"
	smtp.SMTP.Host = "other.example.test"
	_, err = f.backend.UpdatePlatformEmail(ctx, f.meta, smtp)
	servertest.RequireFieldError(t, err, "smtp.password", i18n.FieldSMTPCredentialsIncomplete)
	smtp.SMTP.Password = "replacement"
	_, err = f.backend.UpdatePlatformEmail(ctx, f.meta, smtp)
	require.NoError(t, err)
	require.Equal(t, "replacement", f.state.Current().Settings.Email.SMTP.Password)
	smtp.SMTP.Username = ""
	smtp.SMTP.Password = ""
	_, err = f.backend.UpdatePlatformEmail(ctx, f.meta, smtp)
	require.NoError(t, err)
	require.Empty(t, f.state.Current().Settings.Email.SMTP.Password)
	ses := emailProviderInput(domain.EmailProviderSES)
	_, err = f.backend.UpdatePlatformEmail(ctx, f.meta, ses)
	require.NoError(t, err)
	ses.SES.SecretAccessKey = ""
	_, err = f.backend.UpdatePlatformEmail(ctx, f.meta, ses)
	require.NoError(t, err)
	require.Equal(t, "ses-secret", f.state.Current().Settings.Email.SES.SecretAccessKey)
	ses.SES.AccessKeyID = "changed"
	_, err = f.backend.UpdatePlatformEmail(ctx, f.meta, ses)
	servertest.RequireFieldError(t, err, "ses.secretAccessKey", i18n.FieldEmailSettingInvalid)
	resend := emailProviderInput(domain.EmailProviderResend)
	resend.Resend.APIKey = ""
	_, err = f.backend.UpdatePlatformEmail(ctx, f.meta, resend)
	servertest.RequireFieldError(t, err, "resend.apiKey", i18n.FieldEmailSettingInvalid)
	resend.Enabled = false
	_, err = f.backend.UpdatePlatformEmail(ctx, f.meta, resend)
	require.NoError(t, err)
	require.Nil(t, f.state.Current().Settings.Email.SES)
	resend.Enabled = true
	_, err = f.backend.UpdatePlatformEmail(ctx, f.meta, resend)
	servertest.RequireFieldError(t, err, "resend.apiKey", i18n.FieldEmailSettingInvalid)
	resend.Resend.APIKey = "initial"
	_, err = f.backend.UpdatePlatformEmail(ctx, f.meta, resend)
	require.NoError(t, err)
	// 并发的留空保存与密钥替换均在行锁内合并。
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, key := range []string{"", "concurrent-replacement"} {
		wg.Go(func() {
			input := emailProviderInput(domain.EmailProviderResend)
			input.Resend.APIKey = key
			_, err := f.backend.UpdatePlatformEmail(ctx, f.meta, input)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	require.NoError(t, f.state.Reload(ctx))
	require.Equal(t, "concurrent-replacement", f.state.Current().Settings.Email.Resend.APIKey)
}

// mailTransportFunc 将本地 HTTP 服务接入邮件客户端。
type mailTransportFunc func(*http.Request) (*http.Response, error)

// RoundTrip 按测试场景处理一次 HTTP 请求。
func (f mailTransportFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// TestEmailProviderDelivery 通过数据库保存的配置验证服务商请求及响应语义。
func TestEmailProviderDelivery(t *testing.T) {
	t.Parallel()
	f := newEmailFixture(t)
	ctx := context.Background()
	message := mail.Message{To: "recipient@example.test", Subject: "Hello", Text: "Plain message", HTML: "<p>Hello</p>", FromName: "Business"}
	for _, provider := range []domain.EmailProvider{domain.EmailProviderSES, domain.EmailProviderMailgun, domain.EmailProviderPostmark, domain.EmailProviderResend} {
		t.Run(string(provider), func(t *testing.T) {
			_, err := f.backend.UpdatePlatformEmail(ctx, f.meta, emailProviderInput(provider))
			require.NoError(t, err)
			config := f.state.Current().Settings.Email
			var captured *http.Request
			var payload map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured = r.Clone(r.Context())
				if provider == domain.EmailProviderMailgun {
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Error(err)
					}
					payload = map[string]any{"from": r.FormValue("from"), "to": r.FormValue("to"), "text": r.FormValue("text"), "html": r.FormValue("html")}
				} else if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				switch provider {
				case domain.EmailProviderSES:
					fmt.Fprint(w, `{"MessageId":"accepted"}`)
				case domain.EmailProviderPostmark:
					fmt.Fprint(w, `{"MessageID":"accepted","ErrorCode":0}`)
				default:
					fmt.Fprint(w, `{"id":"accepted"}`)
				}
			}))
			defer server.Close()
			transport := mailTransportFunc(func(r *http.Request) (*http.Response, error) {
				r = r.Clone(r.Context())
				r.URL.Scheme = "http"
				r.URL.Host = strings.TrimPrefix(server.URL, "http://")
				return http.DefaultTransport.RoundTrip(r)
			})
			require.NoError(t, mail.NewClient(config, &http.Client{Transport: transport}).Send(ctx, message))
			require.Equal(t, http.MethodPost, captured.Method)
			switch provider {
			case domain.EmailProviderSES:
				require.Equal(t, "/v2/email/outbound-emails", captured.URL.Path)
				require.Contains(t, captured.Header.Get("Authorization"), "Credential=test-access-key/")
				require.Contains(t, payload["FromEmailAddress"], "Support")
				require.Equal(t, []any{message.To}, payload["Destination"].(map[string]any)["ToAddresses"])
				content := payload["Content"].(map[string]any)["Simple"].(map[string]any)
				body := content["Body"].(map[string]any)
				require.Equal(t, message.Text, body["Text"].(map[string]any)["Data"])
				require.Equal(t, message.HTML, body["Html"].(map[string]any)["Data"])
			case domain.EmailProviderMailgun:
				require.Equal(t, "/v3/mg.example.test/messages", captured.URL.Path)
				username, password, ok := captured.BasicAuth()
				require.True(t, ok)
				require.Equal(t, "api", username)
				require.Equal(t, "mailgun-secret", password)
				require.Contains(t, payload["from"], "Support")
				require.Equal(t, message.Text, payload["text"])
				require.Equal(t, message.HTML, payload["html"])
			case domain.EmailProviderPostmark:
				require.Equal(t, "/email", captured.URL.Path)
				require.Equal(t, "postmark-secret", captured.Header.Get("X-Postmark-Server-Token"))
				require.Equal(t, "transactional", payload["MessageStream"])
				require.Contains(t, payload["From"], "Support")
				require.Equal(t, message.Text, payload["TextBody"])
				require.Equal(t, message.HTML, payload["HtmlBody"])
			case domain.EmailProviderResend:
				require.Equal(t, "/emails", captured.URL.Path)
				require.Equal(t, "Bearer resend-secret", captured.Header.Get("Authorization"))
				require.Equal(t, []any{message.To}, payload["to"])
				require.Contains(t, payload["from"], "Support")
				require.Equal(t, message.Text, payload["text"])
				require.Equal(t, message.HTML, payload["html"])
			}
			// 无明确接受结果、重定向和服务商错误均失败，错误内容不带响应正文。
			for _, status := range []int{200, 302, 401, 429, 500} {
				count := 0
				client := mail.NewClient(config, &http.Client{Transport: mailTransportFunc(func(r *http.Request) (*http.Response, error) {
					count++
					return &http.Response{StatusCode: status, Header: http.Header{"Location": []string{"https://other.example.test"}}, Body: io.NopCloser(strings.NewReader(`{"message":"secret recipient@example.test"}`)), Request: r}, nil
				})})
				err := client.Send(ctx, message)
				require.Error(t, err)
				require.NotContains(t, err.Error(), "secret")
				require.NotContains(t, err.Error(), message.To)
				require.Equal(t, 1, count)
			}
			timeout, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
			defer cancel()
			client := mail.NewClient(config, &http.Client{Transport: mailTransportFunc(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })})
			require.ErrorIs(t, client.Send(timeout, message), context.DeadlineExceeded)
		})
	}
}

// startEmailSMTPServer 启动只在本机接受邮件的 SMTP 服务并采集正文。
func startEmailSMTPServer(t *testing.T, greeting string) (string, int, <-chan string) {
	t.Helper()
	if greeting == "" {
		greeting = "220 localhost ESMTP"
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	messages := make(chan string, 4)
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
				protocol := textproto.NewConn(connection)
				_ = protocol.PrintfLine("%s", greeting)
				for {
					line, err := protocol.ReadLine()
					if err != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"):
						_ = protocol.PrintfLine("250 localhost")
					case strings.HasPrefix(line, "MAIL FROM"), strings.HasPrefix(line, "RCPT TO"), line == "RSET", line == "NOOP":
						_ = protocol.PrintfLine("250 OK")
					case line == "DATA":
						_ = protocol.PrintfLine("354 End with dot")
						body, err := io.ReadAll(protocol.DotReader())
						if err != nil {
							return
						}
						messages <- string(body)
						_ = protocol.PrintfLine("250 Accepted")
					case line == "QUIT":
						_ = protocol.PrintfLine("221 Bye")
						return
					default:
						_ = protocol.PrintfLine("500 Unsupported")
					}
				}
			})
		}
	})
	t.Cleanup(func() { _ = listener.Close(); wg.Wait() })
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	return host, port, messages
}

// TestSavedEmailSMTP 验证测试接口、业务发送器、关闭配置和权限均使用真实保存状态。
func TestSavedEmailSMTP(t *testing.T) {
	t.Parallel()
	f := newEmailFixture(t)
	ctx := context.Background()
	host, port, messages := startEmailSMTPServer(t, "")
	input := emailProviderInput(domain.EmailProviderSMTP)
	input.SMTP = &appservice.PlatformEmailSMTPInput{Host: host, Port: port, Security: "none"}
	_, err := f.backend.UpdatePlatformEmail(ctx, f.meta, input)
	require.NoError(t, err)
	require.NoError(t, f.backend.TestPlatformEmail(ctx, appservice.RequestMeta{AdminToken: f.meta.AdminToken, Locale: domain.LocaleEnglishUnitedStates}, appservice.PlatformEmailTestInput{To: "recipient@example.test"}))
	select {
	case body := <-messages:
		require.Contains(t, body, "Email delivery test")
		require.Contains(t, body, "multipart/alternative")
		require.Contains(t, body, "Support")
	case <-time.After(time.Second):
		t.Fatal("test message was not received")
	}
	require.NoError(t, f.state.Mailer().Send(ctx, mail.Message{To: "recipient@example.test", Subject: "Business message", Text: "Hello", HTML: "<p>Hello</p>"}))
	select {
	case body := <-messages:
		require.Contains(t, body, "Business message")
	case <-time.After(time.Second):
		t.Fatal("business message was not received")
	}
	_, err = f.backend.UpdatePlatformEmail(ctx, appservice.RequestMeta{}, input)
	require.Error(t, err)
	require.Error(t, f.backend.TestPlatformEmail(ctx, appservice.RequestMeta{}, appservice.PlatformEmailTestInput{To: "recipient@example.test"}))
	input.Enabled = false
	_, err = f.backend.UpdatePlatformEmail(ctx, f.meta, input)
	require.NoError(t, err)
	require.ErrorIs(t, f.state.Mailer().Send(ctx, mail.Message{}), deploymentaction.ErrMailDisabled)
	require.True(t, errors.Is(mail.NewClient(mail.Config{}, nil).Send(ctx, mail.Message{}), mail.ErrDisabled))
}

// TestEmailTestFailures 验证配置与外部服务错误使用可预期的业务错误类别。
func TestEmailTestFailures(t *testing.T) {
	t.Parallel()
	f := newEmailFixture(t)
	for _, scenario := range []struct {
		code int
		key  i18n.Key
		kind appservice.ErrorKind
	}{
		{535, i18n.ErrorMailAuthentication, appservice.ErrorKindInvalid},
		{550, i18n.ErrorMailRejected, appservice.ErrorKindInvalid},
		{421, i18n.ErrorMailTestFailed, appservice.ErrorKindUnavailable},
	} {
		t.Run(strconv.Itoa(scenario.code), func(t *testing.T) {
			host, port, _ := startEmailSMTPServer(t, fmt.Sprintf("%d secret recipient@example.test", scenario.code))
			input := emailProviderInput(domain.EmailProviderSMTP)
			input.SMTP = &appservice.PlatformEmailSMTPInput{Host: host, Port: port, Security: "none"}
			_, err := f.backend.UpdatePlatformEmail(context.Background(), f.meta, input)
			require.NoError(t, err)
			err = f.backend.TestPlatformEmail(context.Background(), f.meta, appservice.PlatformEmailTestInput{To: "recipient@example.test"})
			servertest.RequireLocalizedError(t, err, scenario.key)
			var apiError *appservice.Error
			require.ErrorAs(t, err, &apiError)
			require.Equal(t, scenario.kind, apiError.Kind)
			require.Nil(t, apiError.Unwrap())
			require.NotContains(t, err.Error(), "secret")
		})
	}
}

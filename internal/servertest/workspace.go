//go:build server

package servertest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"uuid"

	accountaction "github.com/runforyou-ai/luway/internal/actions/account"
	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	workspaceaction "github.com/runforyou-ai/luway/internal/actions/workspace"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/mail"
	commonpassword "github.com/runforyou-ai/luway/pkg/password"
	"github.com/runforyou-ai/support/str"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// WorkspaceSpec 定义测试工作区名称前缀与首位管理员账号；Email 在同一测试库内必须唯一，语言和时区为空时取中文与上海时区。
type WorkspaceSpec struct {
	Initializer workspaceaction.Initializer
	Name        string
	DisplayName string
	Email       string
	Password    string
	Locale      domain.Locale
	TimeZone    string
}

// InstalledWorkspace 表示测试工作区中某个成员的身份及其账号登录令牌。
type InstalledWorkspace struct {
	Identity *servermodels.Identity
	Token    string
}

// InstallWorkspace 创建名称和标识独立的工作区及管理员账号并签发登录会话。
func InstallWorkspace(t testing.TB, db *bun.DB, spec WorkspaceSpec) InstalledWorkspace {
	t.Helper()
	ctx := context.Background()
	spec.Name = UniqueWorkspaceName(spec.Name)
	EnsurePlatform(t, db)
	if spec.Locale == "" {
		spec.Locale = domain.LocaleChineseSimplified
	}
	if spec.TimeZone == "" {
		spec.TimeZone = "Asia/Shanghai"
	}
	passwordHash, err := commonpassword.Hash(spec.Password)
	require.NoError(t, err)
	var token string
	var workspaceID string
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		account, err := accountaction.CreateAccount(ctx, tx, accountaction.NewAccount{
			Email: spec.Email, PasswordHash: passwordHash, DisplayName: spec.DisplayName, Locale: spec.Locale, TimeZone: spec.TimeZone,
		})
		if err != nil {
			return err
		}
		created, err := workspaceaction.Create(ctx, tx, workspaceaction.CreateInput{
			Name: spec.Name, Account: account, AdminDisplayName: spec.DisplayName,
			Initializer: spec.Initializer,
		})
		if err != nil {
			return err
		}
		workspaceID = created.Workspace.ID
		session, err := authaction.IssueSession(ctx, tx, account)
		token = session.Token
		return err
	})
	require.NoError(t, err)
	return ResolveMemberSession(t, db, workspaceID, token)
}

// EnsurePlatform 在共享测试库中写入平台行与部署版本，注册仅限受邀、工作区仅平台管理员可创建，部署地址为 PublicURL；已存在时保留原行。
func EnsurePlatform(t testing.TB, db *bun.DB) {
	t.Helper()
	_, err := db.NewInsert().Model(&servermodels.Platform{
		RegistrationPolicy:      string(domain.RegistrationPolicyInvitationOnly),
		WorkspaceCreationPolicy: string(domain.WorkspaceCreationPolicyPlatformAdmin),
		PublicURL:               PublicURL,
	}).Column("registration_policy", "workspace_creation_policy", "public_url").On("CONFLICT DO NOTHING").Exec(context.Background())
	require.NoError(t, err)
}

// TestDeployment 写入测试平台行后返回已读取的部署状态，部署地址为 PublicURL。
func TestDeployment(t testing.TB, db *bun.DB) *deploymentaction.DeploymentState {
	t.Helper()
	EnsurePlatform(t, db)
	return NewDeployment(t, db)
}

// UniqueWorkspaceName 为共享测试库中的工作区名称添加随机后缀。
func UniqueWorkspaceName(prefix string) string {
	return str.Substr(prefix, 0, domain.WorkspaceNameMaxLength-13) + "-" + UniqueSuffix()
}

// UniqueSuffix 返回 12 位随机十六进制串，用作共享测试库中名称的唯一后缀。
func UniqueSuffix() string {
	return strings.ReplaceAll(uuid.NewV7().String(), "-", "")[20:]
}

// AddAccountWorkspace 为登录令牌所属账号创建名称和标识独立的工作区，账号成为首位管理员成员。
func AddAccountWorkspace(t testing.TB, db *bun.DB, token, name string) *servermodels.Identity {
	t.Helper()
	ctx := context.Background()
	account, err := authaction.NewResolveAccountQuery(db).Execute(ctx, token)
	require.NoError(t, err, "resolve account")
	var created *servermodels.Identity
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		created, err = workspaceaction.Create(ctx, tx, workspaceaction.CreateInput{
			Name: UniqueWorkspaceName(name), Account: &account.Account, AdminDisplayName: account.Account.DisplayName,
		})
		return err
	})
	require.NoError(t, err)
	return created
}

// LoginMember 用账号密码登录，并解析该账号在目标工作区中的成员身份。
func LoginMember(t testing.TB, db *bun.DB, workspaceID, email, password string) InstalledWorkspace {
	t.Helper()
	session, err := authaction.NewLoginAction(db).Execute(context.Background(), authaction.LoginInput{Email: email, Password: password})
	require.NoError(t, err, "login %s", email)
	return ResolveMemberSession(t, db, workspaceID, session.Token)
}

// ResolveMemberSession 解析登录令牌在目标工作区中的成员身份。
func ResolveMemberSession(t testing.TB, db *bun.DB, workspaceID, token string) InstalledWorkspace {
	t.Helper()
	identity, err := authaction.NewResolveIdentityQuery(db).Execute(context.Background(), workspaceID, token)
	require.NoError(t, err, "resolve member session")
	return InstalledWorkspace{Identity: identity, Token: token}
}

// UniqueEmail 返回同一测试库内唯一的邮箱，local 用于标识测试中的角色。
func UniqueEmail(local string) string {
	return local + "." + strings.ReplaceAll(uuid.NewV7().String(), "-", "") + "@example.test"
}

// DisabledMail 是部署未配置邮件发送时的发信器。
type DisabledMail struct{}

// Enabled 返回部署未配置邮件发送。
func (DisabledMail) Enabled() bool {
	return false
}

// Send 返回邮件发送未配置的错误。
func (DisabledMail) Send(context.Context, mail.Message) error {
	return deploymentaction.ErrMailDisabled
}

// RecordingMailSender 记录发出的邮件，Fail 非空时返回该错误。
type RecordingMailSender struct {
	mu       sync.Mutex
	messages []mail.Message
	Fail     error
}

// Enabled 返回部署已配置邮件发送。
func (s *RecordingMailSender) Enabled() bool {
	return true
}

// Send 记录邮件或返回预设错误。
func (s *RecordingMailSender) Send(_ context.Context, message mail.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Fail != nil {
		return s.Fail
	}
	s.messages = append(s.messages, message)
	return nil
}

// Sent 返回已记录的邮件。
func (s *RecordingMailSender) Sent() []mail.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mail.Message(nil), s.messages...)
}

// RequireErrorKind 断言错误是指定种类的业务错误。
func RequireErrorKind(t *testing.T, err error, want appservice.ErrorKind) {
	t.Helper()
	appErr, ok := errors.AsType[*appservice.Error](err)
	require.True(t, ok, "error = %#v", err)
	require.Equal(t, want, appErr.Kind)
}

// RequireFieldError 断言错误是指定字段的本地化校验失败。
func RequireFieldError(t *testing.T, err error, field string, key i18n.Key) {
	t.Helper()
	appErr, ok := errors.AsType[*appservice.Error](err)
	want, _ := i18n.Localize("zh-CN", key)
	require.True(t, ok, "error = %#v", err)
	require.Equal(t, want, appErr.Fields[field], "field %s", field)
}

// RequireErrorMessage 断言错误是指定文案的业务错误。
func RequireErrorMessage(t *testing.T, err error, key i18n.Key) {
	t.Helper()
	want, _ := i18n.Localize("zh-CN", key)
	appErr, ok := errors.AsType[*appservice.Error](err)
	require.True(t, ok, "error = %#v, want message %q", err, want)
	require.Equal(t, want, appErr.Message)
}

// RequireLocalizedError 断言错误是按简体中文本地化的指定业务错误。
func RequireLocalizedError(t *testing.T, err error, key i18n.Key) {
	t.Helper()
	want, _ := i18n.Localize("zh-CN", key)
	appErr, ok := errors.AsType[*appservice.Error](err)
	require.True(t, ok, "error = %v, want %s（%s）", err, key, want)
	require.Equal(t, want, appErr.Message)
}

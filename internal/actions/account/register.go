//go:build server

package account

import (
	"context"
	"errors"
	"fmt"
	"strings"

	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	invitationaction "github.com/runforyou-ai/luway/internal/actions/invitation"
	platformaction "github.com/runforyou-ai/luway/internal/actions/platform"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	commonpassword "github.com/runforyou-ai/luway/pkg/password"
	"github.com/uptrace/bun"
)

var (
	// ErrRegistrationClosed 表示平台注册策略为仅限受邀，且注册未携带邀请令牌。
	ErrRegistrationClosed = errors.New("account registration is closed")
	// ErrInstallationRequired 表示平台尚未完成首次安装，第一个账号只能由首次安装创建。
	ErrInstallationRequired = errors.New("platform installation is required")
)

// RegisterAction 注册本地账号并签发登录会话。
type RegisterAction struct {
	db *bun.DB
}

// NewRegisterAction 创建本地账号注册操作。
func NewRegisterAction(db *bun.DB) *RegisterAction {
	return &RegisterAction{db: db}
}

// Execute 在平台已完成首次安装时校验字段、创建账号并签发登录会话；平台注册策略为仅限受邀时只接受带有效邀请令牌且邮箱与受邀邮箱一致的注册。
func (a *RegisterAction) Execute(ctx context.Context, input NewAccountInput, invitationToken string) (authaction.SessionOutput, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if fields := ValidateNewAccount(input); len(fields) > 0 {
		return authaction.SessionOutput{}, &ValidationError{Fields: fields}
	}
	passwordHash, err := commonpassword.Hash(input.Password)
	if err != nil {
		return authaction.SessionOutput{}, fmt.Errorf("hash account password: %w", err)
	}
	var output authaction.SessionOutput
	err = serverstorage.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		platform, err := platformaction.Load(ctx, tx)
		if errors.Is(err, platformaction.ErrNotInstalled) {
			return ErrInstallationRequired
		}
		if err != nil {
			return err
		}
		if platform.RegistrationPolicy != string(domain.RegistrationPolicyOpen) && invitationToken == "" {
			return ErrRegistrationClosed
		}
		if invitationToken != "" {
			invitedEmail, err := invitationaction.PendingEmail(ctx, tx, invitationToken)
			if err != nil {
				return err
			}
			if !strings.EqualFold(invitedEmail, input.Email) {
				return invitationaction.ErrEmailMismatch
			}
		}
		created, err := CreateAccount(ctx, tx, NewAccount{
			Email: input.Email, PasswordHash: passwordHash, DisplayName: input.DisplayName, Locale: input.Locale, TimeZone: input.TimeZone,
		})
		if errors.Is(err, ErrAccountEmailTaken) {
			return &ValidationError{Fields: map[string]ValidationCode{"email": ValidationEmailDuplicate}}
		}
		if err != nil {
			return err
		}
		output, err = authaction.IssueSession(ctx, tx, created)
		return err
	})
	if err != nil {
		return authaction.SessionOutput{}, err
	}
	return output, nil
}

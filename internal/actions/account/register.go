//go:build server

package account

import (
	"context"
	"errors"
	"fmt"
	"strings"

	authaction "github.com/runforyou-ai/cervi/internal/actions/auth"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	invitationaction "github.com/runforyou-ai/cervi/internal/actions/invitation"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	commonemail "github.com/runforyou-ai/cervi/pkg/email"
	commonpassword "github.com/runforyou-ai/cervi/pkg/password"
	"github.com/uptrace/bun"
)

var (
	// ErrRegistrationClosed 表示部署未开放账号注册。
	ErrRegistrationClosed = errors.New("account registration is closed")
	// ErrInstallationRequired 表示部署尚未完成首次安装，第一个账号只能由首次安装创建。
	ErrInstallationRequired = errors.New("deployment installation is required")
)

// RegisterAction 注册本地账号并签发登录会话。
type RegisterAction struct {
	db   *bun.DB
	open bool
}

// NewRegisterAction 创建本地账号注册操作，open 为部署配置的注册开关。
func NewRegisterAction(db *bun.DB, open bool) *RegisterAction {
	return &RegisterAction{db: db, open: open}
}

// Execute 在部署已完成首次安装时校验字段、创建账号并签发登录会话；部署未开放注册时只接受带有效邀请令牌且邮箱与受邀邮箱一致的注册。
func (a *RegisterAction) Execute(ctx context.Context, input NewAccountInput, invitationToken string) (authaction.SessionOutput, error) {
	if !a.open && invitationToken == "" {
		return authaction.SessionOutput{}, ErrRegistrationClosed
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Email = commonemail.Normalize(input.Email)
	if fields := ValidateNewAccount(input); len(fields) > 0 {
		return authaction.SessionOutput{}, &ValidationError{Fields: fields}
	}
	passwordHash, err := commonpassword.Hash(input.Password)
	if err != nil {
		return authaction.SessionOutput{}, fmt.Errorf("hash account password: %w", err)
	}
	var output authaction.SessionOutput
	err = a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// 账号只增不删，已存在任一账号即表示首次安装已提交。
		installed, err := tx.NewSelect().Model((*servermodels.Account)(nil)).Exists(ctx)
		if err != nil {
			return err
		}
		if !installed {
			return ErrInstallationRequired
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
		created, err := identityaction.CreateAccount(ctx, tx, identityaction.NewAccount{
			Email: input.Email, PasswordHash: passwordHash, DisplayName: input.DisplayName, Locale: input.Locale, TimeZone: input.TimeZone,
		})
		if errors.Is(err, identityaction.ErrAccountEmailTaken) {
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

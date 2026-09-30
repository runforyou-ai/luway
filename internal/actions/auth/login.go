//go:build server

package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	commonemail "github.com/runforyou-ai/cervi/pkg/email"
	commonpassword "github.com/runforyou-ai/cervi/pkg/password"
	"github.com/uptrace/bun"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

// LoginAction 执行账号密码登录。
type LoginAction struct {
	db *bun.DB
}

// LoginInput 定义登录操作输入。
type LoginInput struct {
	Email    string
	Password string
}

// NewLoginAction 创建账号密码登录操作。
func NewLoginAction(db *bun.DB) *LoginAction {
	return &LoginAction{db: db}
}

// Execute 校验有效账号的邮箱和密码并签发登录会话。
func (a *LoginAction) Execute(ctx context.Context, input LoginInput) (SessionOutput, error) {
	account := &servermodels.Account{}
	err := a.db.NewSelect().Model(account).
		Where("lower(acc.email) = lower(?)", commonemail.Normalize(input.Email)).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionOutput{}, ErrInvalidCredentials
	}
	if err != nil {
		return SessionOutput{}, fmt.Errorf("find account: %w", err)
	}
	if account.Status != string(domain.AccountStatusActive) || !commonpassword.Matches(account.PasswordHash, input.Password) {
		return SessionOutput{}, ErrInvalidCredentials
	}
	output, err := IssueSession(ctx, a.db, account)
	if err != nil {
		return SessionOutput{}, fmt.Errorf("complete login: %w", err)
	}
	return output, nil
}

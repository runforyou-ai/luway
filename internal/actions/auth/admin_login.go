//go:build server

package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	commonpassword "github.com/runforyou-ai/luway/pkg/password"
	"github.com/uptrace/bun"
)

// AdminLoginAction 执行平台管理员后台登录。
type AdminLoginAction struct {
	db *bun.DB
}

// NewAdminLoginAction 创建平台后台登录操作。
func NewAdminLoginAction(db *bun.DB) *AdminLoginAction {
	return &AdminLoginAction{db: db}
}

// Execute 校验平台管理员密码并签发后台会话。
func (a *AdminLoginAction) Execute(ctx context.Context, input LoginInput) (SessionOutput, error) {
	account := &servermodels.Account{}
	err := a.db.NewSelect().Model(account).
		Where("lower(acc.email) = lower(?)", strings.ToLower(strings.TrimSpace(input.Email))).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionOutput{}, ErrInvalidCredentials
	}
	if err != nil {
		return SessionOutput{}, fmt.Errorf("find account: %w", err)
	}
	if account.Status != string(domain.AccountStatusActive) || !account.IsPlatformAdmin || !commonpassword.Matches(account.PasswordHash, input.Password) {
		return SessionOutput{}, ErrInvalidCredentials
	}
	output, err := IssueAdminSession(ctx, a.db, account)
	if err != nil {
		return SessionOutput{}, fmt.Errorf("complete admin login: %w", err)
	}
	return output, nil
}

//go:build server

// Package auth 实现本地与官方账号登录、登录会话和请求身份解析。
package auth

import (
	"context"
	"fmt"
	"time"

	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/runforyou-ai/cervi/pkg/token"
	"github.com/uptrace/bun"
)

// sessionValidity 是登录会话令牌从签发起的有效期。
const sessionValidity = 30 * 24 * time.Hour

// SessionOutput 返回登录账号和新签发的会话令牌。
type SessionOutput struct {
	Account   *servermodels.Account
	Token     string
	ExpiresAt time.Time
}

// IssueSession 在调用方事务内为账号签发登录会话令牌。
func IssueSession(ctx context.Context, db bun.IDB, account *servermodels.Account) (SessionOutput, error) {
	issued, err := token.Issue(sessionValidity)
	if err != nil {
		return SessionOutput{}, fmt.Errorf("issue session token: %w", err)
	}
	session := &servermodels.AccountSession{AccountID: account.ID, TokenHash: issued.TokenHash, ExpiresAt: issued.ExpiresAt}
	if _, err := db.NewInsert().Model(session).Column("account_id", "token_hash", "expires_at").Exec(ctx); err != nil {
		return SessionOutput{}, fmt.Errorf("save account session: %w", err)
	}
	return SessionOutput{Account: account, Token: issued.Token, ExpiresAt: issued.ExpiresAt}, nil
}

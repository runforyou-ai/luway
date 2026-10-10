//go:build server

// Package auth 实现账号登录、登录会话和请求身份解析。
package auth

import (
	"context"
	"fmt"
	"time"

	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/random"
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

// IssueSession 经调用方给出的连接或事务为账号签发登录会话令牌。
func IssueSession(ctx context.Context, db bun.IDB, account *servermodels.Account) (SessionOutput, error) {
	token, tokenHash := random.Token(32)
	session := &servermodels.AccountSession{AccountID: account.ID, TokenHash: tokenHash}
	if _, err := db.NewInsert().Model(session).Column("account_id", "token_hash", "expires_at").
		Value("expires_at", "now() + make_interval(secs => ?)", sessionValidity.Seconds()).
		Returning("expires_at").Exec(ctx); err != nil {
		return SessionOutput{}, fmt.Errorf("save account session: %w", err)
	}
	return SessionOutput{Account: account, Token: token, ExpiresAt: session.ExpiresAt}, nil
}

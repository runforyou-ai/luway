//go:build server

// Package auth 实现账号登录、登录会话和请求身份解析。
package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/random"
	"github.com/uptrace/bun"
)

const (
	// userSessionValidity 是用户端会话从签发起的有效期。
	userSessionValidity = 30 * 24 * time.Hour
	// adminSessionValidity 是平台后台会话从签发起的有效期。
	adminSessionValidity = 12 * time.Hour
	// supportSessionValidity 是支援进入会话从签发起的有效期。
	supportSessionValidity = 8 * time.Hour
)

// SessionOutput 返回登录账号和新签发的会话令牌。
type SessionOutput struct {
	Account   *servermodels.Account
	SessionID string
	Token     string
	ExpiresAt time.Time
	Kind      domain.SessionKind
}

// IssueUserSession 为账号签发用户端 Bearer 会话。
func IssueUserSession(ctx context.Context, db bun.IDB, account *servermodels.Account) (SessionOutput, error) {
	return issueSession(ctx, db, account, domain.SessionKindUser, nil, userSessionValidity)
}

// IssueAdminSession 为平台管理员签发后台 Cookie 会话。
func IssueAdminSession(ctx context.Context, db bun.IDB, account *servermodels.Account) (SessionOutput, error) {
	if !account.IsPlatformAdmin {
		return SessionOutput{}, fmt.Errorf("issue admin session: account is not platform admin")
	}
	return issueSession(ctx, db, account, domain.SessionKindAdmin, nil, adminSessionValidity)
}

// IssueSupportSession 为平台管理员签发绑定目标工作区的短期支援 Bearer 会话。
func IssueSupportSession(ctx context.Context, db bun.IDB, account *servermodels.Account, workspaceID string) (SessionOutput, error) {
	if !account.IsPlatformAdmin {
		return SessionOutput{}, fmt.Errorf("issue support session: account is not platform admin")
	}
	if workspaceID == "" {
		return SessionOutput{}, fmt.Errorf("issue support session: workspace id required")
	}
	return issueSession(ctx, db, account, domain.SessionKindSupport, &workspaceID, supportSessionValidity)
}

// IssueSession 签发用户端会话，供注册与登录等用户面入口调用。
func IssueSession(ctx context.Context, db bun.IDB, account *servermodels.Account) (SessionOutput, error) {
	return IssueUserSession(ctx, db, account)
}

func issueSession(ctx context.Context, db bun.IDB, account *servermodels.Account, kind domain.SessionKind, workspaceID *string, validity time.Duration) (SessionOutput, error) {
	token, tokenHash := random.Token(32)
	session := &servermodels.AccountSession{
		AccountID:   account.ID,
		Kind:        string(kind),
		WorkspaceID: workspaceID,
		TokenHash:   tokenHash,
	}
	columns := []string{"account_id", "kind", "token_hash", "expires_at"}
	if workspaceID != nil {
		columns = append(columns, "workspace_id")
	}
	// expires_at 必须出现在 Column 中，Value 才会覆盖为数据库表达式。
	// Returning 第二个参数起是占位符实参，多列必须写在同一字符串里。
	if err := db.NewInsert().Model(session).Column(columns...).
		Value("expires_at", "now() + make_interval(secs => ?)", validity.Seconds()).
		Returning("id, expires_at").Scan(ctx); err != nil {
		return SessionOutput{}, fmt.Errorf("save account session: %w", err)
	}
	if session.ID == "" || session.ExpiresAt.IsZero() {
		return SessionOutput{}, fmt.Errorf("save account session: missing returned id or expires_at")
	}
	return SessionOutput{
		Account:   account,
		SessionID: session.ID,
		Token:     token,
		ExpiresAt: session.ExpiresAt,
		Kind:      kind,
	}, nil
}

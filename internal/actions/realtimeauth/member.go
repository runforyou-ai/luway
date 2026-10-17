//go:build server

package realtimeauth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	authaction "github.com/runforyou-ai/luway/internal/actions/auth"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// MemberLifetime 是成员接入票据的有效期。
const MemberLifetime = 2 * time.Minute

// IssueMember 用有效登录会话的摘要签发用途隔离的短期接入票据。
func IssueMember(ctx context.Context, db bun.IDB, accountID, sessionID string) (string, error) {
	var secret string
	if err := db.NewRaw("SELECT token_hash FROM account_sessions WHERE id = ? AND account_id = ? AND expires_at > now()", sessionID, accountID).Scan(ctx, &secret); err != nil {
		return "", err
	}
	now, err := serverstorage.ClockNow(ctx, db)
	if err != nil {
		return "", err
	}
	claims := jwt.RegisteredClaims{Subject: sessionID, Audience: []string{"realtime-member"}, ExpiresAt: jwt.NewNumericDate(now.Add(MemberLifetime))}
	value, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("realtime-member:" + secret))
	return "rt_a." + value, err
}

// AuthenticateMember 验证接入票据并重新读取有效账号会话。
func AuthenticateMember(ctx context.Context, db bun.IDB, ticket string) (*servermodels.AccountIdentity, error) {
	raw, ok := strings.CutPrefix(ticket, "rt_a.")
	if !ok {
		return nil, ErrInvalid
	}
	now, err := serverstorage.ClockNow(ctx, db)
	if err != nil {
		return nil, err
	}
	claims := &jwt.RegisteredClaims{}
	var accountID string
	_, err = jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) {
		if !str.IsUUID(claims.Subject) {
			return nil, ErrInvalid
		}
		var secret string
		err := db.NewRaw("SELECT account_id::text, token_hash FROM account_sessions WHERE id = ? AND expires_at > now()", claims.Subject).Scan(ctx, &accountID, &secret)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalid
		}
		return []byte("realtime-member:" + secret), err
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience("realtime-member"), jwt.WithExpirationRequired(), jwt.WithTimeFunc(func() time.Time { return now }))
	if err != nil {
		return nil, err
	}
	if claims.ExpiresAt.Time.After(now.Add(MemberLifetime)) {
		return nil, ErrInvalid
	}
	return authaction.ResolveAccountSession(ctx, db, accountID, claims.Subject)
}

//go:build server

package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// acmeChallengeRetention 是 HTTP-01 质询应答的最长保留时长，超过时长的应答在发布新应答时删除。
const acmeChallengeRetention = time.Hour

// ACMEChallenges 在 PostgreSQL 中发布 HTTP-01 质询应答，部署中的每台服务器都能按令牌读取。
type ACMEChallenges struct {
	db bun.IDB
}

// NewACMEChallenges 创建 HTTP-01 质询应答存储。
func NewACMEChallenges(db bun.IDB) *ACMEChallenges {
	return &ACMEChallenges{db: db}
}

// Put 保存令牌对应的密钥授权，并删除超过保留时长的应答。
func (c *ACMEChallenges) Put(ctx context.Context, token, keyAuthorization string) error {
	if _, err := c.db.NewDelete().Model((*servermodels.ACMEChallenge)(nil)).
		Where("created_at < now() - make_interval(secs => ?)", acmeChallengeRetention.Seconds()).
		Exec(ctx); err != nil {
		return fmt.Errorf("delete expired ACME challenges: %w", err)
	}
	if _, err := c.db.NewInsert().Model(&servermodels.ACMEChallenge{Token: token, KeyAuthorization: keyAuthorization}).
		On("CONFLICT (token) DO UPDATE").
		Set("key_authorization = EXCLUDED.key_authorization").
		Exec(ctx); err != nil {
		return fmt.Errorf("save ACME challenge: %w", err)
	}
	return nil
}

// Delete 删除令牌的应答。
func (c *ACMEChallenges) Delete(ctx context.Context, token string) error {
	if _, err := c.db.NewDelete().Model((*servermodels.ACMEChallenge)(nil)).Where("token = ?", token).Exec(ctx); err != nil {
		return fmt.Errorf("delete ACME challenge: %w", err)
	}
	return nil
}

// KeyAuthorization 返回令牌对应的密钥授权，令牌不存在时 found 为假。
func (c *ACMEChallenges) KeyAuthorization(ctx context.Context, token string) (keyAuthorization string, found bool, err error) {
	err = c.db.NewSelect().Model((*servermodels.ACMEChallenge)(nil)).Column("key_authorization").
		Where("token = ?", token).Scan(ctx, &keyAuthorization)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read ACME challenge: %w", err)
	}
	return keyAuthorization, true, nil
}

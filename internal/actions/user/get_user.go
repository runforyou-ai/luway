//go:build server

package user

import (
	"context"
	"fmt"

	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// GetUserQuery 读取当前企业的成员详情。
type GetUserQuery struct {
	db *bun.DB
}

// NewGetUserQuery 创建企业成员详情查询。
func NewGetUserQuery(db *bun.DB) *GetUserQuery {
	return &GetUserQuery{db: db}
}

// Execute 返回当前企业的指定成员。
func (q *GetUserQuery) Execute(ctx context.Context, identity *servermodels.Identity, userID string) (*User, error) {
	user, err := loadUser(ctx, q.db, identity.Organization.ID, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	return user, nil
}

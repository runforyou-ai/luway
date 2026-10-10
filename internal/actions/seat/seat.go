//go:build server

// Package seat 按工作区的席位上限校验成员启用：启用的成员占一个席位，席位上限由程序组成提供，自托管不限席位。
package seat

import (
	"context"
	"database/sql"
	"errors"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// ErrLimitReached 表示启用的成员数已达到或将超过工作区的席位上限。
var ErrLimitReached = errors.New("workspace seat limit reached")

// Limit 返回工作区的席位上限，0 表示不限；在读取或启用成员的事务内调用。
type Limit func(ctx context.Context, db bun.IDB, workspaceID string) (int, error)

// Usage 定义工作区的席位上限与启用的成员数，Limit 为 0 表示不限。
type Usage struct {
	Limit int
	Used  int
}

// Seats 按席位上限读取与校验工作区的成员启用，零值不限席位。
type Seats struct {
	limit Limit
}

// New 创建按 limit 校验席位的席位校验，limit 为空时不限席位。
func New(limit Limit) Seats {
	return Seats{limit: limit}
}

// Usage 返回工作区的席位上限与启用的成员数。
func (s Seats) Usage(ctx context.Context, db bun.IDB, workspaceID string) (Usage, error) {
	var usage Usage
	if err := db.NewSelect().Model((*servermodels.User)(nil)).
		ColumnExpr("count(*)").
		Where("workspace_id = ? AND status = ?", workspaceID, domain.IdentityStatusActive).
		Scan(ctx, &usage.Used); err != nil {
		return Usage{}, err
	}
	if s.limit == nil {
		return usage, nil
	}
	limit, err := s.limit(ctx, db, workspaceID)
	usage.Limit = limit
	return usage, err
}

// Check 校验工作区还能再启用一名成员，没有空余席位时返回 ErrLimitReached。
func (s Seats) Check(ctx context.Context, db bun.IDB, workspaceID string) error {
	usage, err := s.Usage(ctx, db, workspaceID)
	if err == nil && usage.Limit > 0 && usage.Used >= usage.Limit {
		return ErrLimitReached
	}
	return err
}

// LockWorkspace 以 NO KEY UPDATE 锁定工作区的席位状态并返回工作区是否存在；成员启用与改变席位上限的写入经同一把锁串行。
func LockWorkspace(ctx context.Context, tx bun.Tx, workspaceID string) (bool, error) {
	if !str.IsUUID(workspaceID) {
		return false, nil
	}
	err := tx.NewSelect().Model((*servermodels.Workspace)(nil)).
		ColumnExpr("o.id::text").
		Where("o.id = ?", workspaceID).
		For("NO KEY UPDATE").
		Scan(ctx, new(string))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Enforce 在启用成员的事务中锁定工作区，启用后的成员数超过席位上限时返回 ErrLimitReached；同一工作区的启用操作按锁串行。
func (s Seats) Enforce(ctx context.Context, tx bun.Tx, workspaceID string) error {
	if _, err := LockWorkspace(ctx, tx, workspaceID); err != nil {
		return err
	}
	usage, err := s.Usage(ctx, tx, workspaceID)
	if err == nil && usage.Limit > 0 && usage.Used > usage.Limit {
		return ErrLimitReached
	}
	return err
}

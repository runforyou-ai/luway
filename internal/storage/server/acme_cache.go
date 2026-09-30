//go:build server

package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
	"golang.org/x/crypto/acme/autocert"
)

var _ autocert.Cache = (*ACMECache)(nil)

// ACMECache 使用 PostgreSQL 共享自动 TLS 的证书与账户数据。
type ACMECache struct {
	db *bun.DB
}

// NewACMECache 创建 PostgreSQL 自动 TLS 缓存。
func NewACMECache(db *bun.DB) *ACMECache {
	return &ACMECache{db: db}
}

// Get 返回指定键的缓存数据。
func (c *ACMECache) Get(ctx context.Context, key string) ([]byte, error) {
	entry := &servermodels.ACMECacheEntry{}
	err := c.db.NewSelect().
		Model(entry).
		Column("data").
		Where("ace.cache_key = ?", key).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, autocert.ErrCacheMiss
	}
	if err != nil {
		return nil, fmt.Errorf("read ACME cache: %w", err)
	}
	return entry.Data, nil
}

// Put 保存指定键的缓存数据。
func (c *ACMECache) Put(ctx context.Context, key string, data []byte) error {
	entry := &servermodels.ACMECacheEntry{CacheKey: key, Data: data}
	_, err := c.db.NewInsert().
		Model(entry).
		Column("cache_key", "data").
		On("CONFLICT (cache_key) DO UPDATE").
		Set("data = EXCLUDED.data").
		Set("updated_at = now()").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("write ACME cache: %w", err)
	}
	return nil
}

// Delete 删除指定键的缓存数据。
func (c *ACMECache) Delete(ctx context.Context, key string) error {
	_, err := c.db.NewDelete().
		Model((*servermodels.ACMECacheEntry)(nil)).
		Where("cache_key = ?", key).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("delete ACME cache: %w", err)
	}
	return nil
}

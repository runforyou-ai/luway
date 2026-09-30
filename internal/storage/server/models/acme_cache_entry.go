//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ACMECacheEntry 表示自动 TLS 使用的一项共享缓存数据。
type ACMECacheEntry struct {
	bun.BaseModel `bun:"table:acme_cache_entries,alias:ace"`

	CacheKey  string    `bun:"cache_key,pk"`
	Data      []byte    `bun:"data"`
	UpdatedAt time.Time `bun:"updated_at"`
	CreatedAt time.Time `bun:"created_at,nullzero,default:now()"`
}

-- +goose Up
-- 创建自动 TLS 共享缓存表。
CREATE TABLE acme_cache_entries (
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    cache_key   text PRIMARY KEY,
    data        bytea NOT NULL
);

COMMENT ON TABLE acme_cache_entries IS '自动 TLS 共享缓存';
COMMENT ON COLUMN acme_cache_entries.created_at IS '创建时间';
COMMENT ON COLUMN acme_cache_entries.updated_at IS '更新时间';
COMMENT ON COLUMN acme_cache_entries.cache_key IS 'ACME 缓存键';
COMMENT ON COLUMN acme_cache_entries.data IS 'ACME 不透明缓存数据';

-- +goose Down
DROP TABLE acme_cache_entries;

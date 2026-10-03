-- +goose Up
-- 平台记录与 control 同步的结果。
ALTER TABLE platforms
    ADD COLUMN control_synced_at timestamptz,
    ADD COLUMN control_failed_at timestamptz,
    ADD COLUMN control_error text NOT NULL DEFAULT '';

COMMENT ON COLUMN platforms.control_synced_at IS '最近一次与 control 同步成功的时间，从未成功时为空';
COMMENT ON COLUMN platforms.control_failed_at IS '最近一次与 control 同步失败的时间，此后同步成功时清空';
COMMENT ON COLUMN platforms.control_error IS '最近一次与 control 同步失败的原因，此后同步成功时清空';

-- +goose Down
ALTER TABLE platforms
    DROP COLUMN control_error,
    DROP COLUMN control_failed_at,
    DROP COLUMN control_synced_at;

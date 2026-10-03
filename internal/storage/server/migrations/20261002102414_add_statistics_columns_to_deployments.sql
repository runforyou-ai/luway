-- +goose Up
-- 部署增加运营数据统计时区与重建标记。
ALTER TABLE deployments
    ADD COLUMN statistics_time_zone text NOT NULL DEFAULT 'UTC',
    ADD COLUMN statistics_rebuild_pending boolean NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN deployments.statistics_time_zone IS '运营数据按日统计使用的 IANA 时区';
COMMENT ON COLUMN deployments.statistics_rebuild_pending IS '修改统计时区后等待按新时区全部重建运营数据';

-- +goose Down
ALTER TABLE deployments
    DROP COLUMN statistics_rebuild_pending,
    DROP COLUMN statistics_time_zone;

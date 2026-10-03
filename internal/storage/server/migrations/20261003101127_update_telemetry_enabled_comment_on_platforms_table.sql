-- +goose Up
-- 上报开关同时控制运行指标与错误上报。
COMMENT ON COLUMN platforms.telemetry_enabled IS '是否向 control 上报运行指标与错误';

-- +goose Down
COMMENT ON COLUMN platforms.telemetry_enabled IS '是否向 control 上报运行指标';

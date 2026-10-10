-- +goose Up
-- 创建公开入口的请求限速表。
CREATE TABLE rate_limits (
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    key         text PRIMARY KEY,
    tat         timestamptz NOT NULL
);

CREATE INDEX rate_limits_tat_idx
    ON rate_limits (tat);

CREATE TRIGGER rate_limits_set_updated_at BEFORE UPDATE ON rate_limits FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE rate_limits IS '公开入口按限速键记录的请求额度，按 GCRA 算法计算';
COMMENT ON COLUMN rate_limits.created_at IS '创建时间';
COMMENT ON COLUMN rate_limits.updated_at IS '更新时间';
COMMENT ON COLUMN rate_limits.key IS '限速键：规则名称与限速对象';
COMMENT ON COLUMN rate_limits.tat IS '理论到达时间：早于当前时刻时额度已全部恢复';

-- +goose Down
DROP TABLE rate_limits;

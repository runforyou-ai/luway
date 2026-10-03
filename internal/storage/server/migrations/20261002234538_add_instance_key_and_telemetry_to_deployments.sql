-- +goose Up
-- 部署增加实例签名私钥与运行指标上报开关，签名私钥由数据库随机生成。
CREATE EXTENSION IF NOT EXISTS pgcrypto;

ALTER TABLE deployments
    ADD COLUMN instance_private_key bytea NOT NULL DEFAULT gen_random_bytes(32),
    ADD COLUMN telemetry_enabled boolean NOT NULL DEFAULT TRUE;

COMMENT ON COLUMN deployments.instance_id IS '实例标识，首次安装或重置实例时生成';
COMMENT ON COLUMN deployments.instance_private_key IS '实例签名私钥（Ed25519 种子），用于向 control 证明实例身份，首次安装或重置实例时生成';
COMMENT ON COLUMN deployments.telemetry_enabled IS '是否向 control 上报运行指标';

-- +goose Down
COMMENT ON COLUMN deployments.instance_id IS '实例标识，首次安装时生成，之后不变';

ALTER TABLE deployments
    DROP COLUMN telemetry_enabled,
    DROP COLUMN instance_private_key;

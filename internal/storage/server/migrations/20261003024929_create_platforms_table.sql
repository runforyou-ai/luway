-- +goose Up
-- 创建平台表。
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE platforms (
    server_id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    registration_policy         text NOT NULL,
    workspace_creation_policy   text NOT NULL,
    statistics_time_zone        text NOT NULL DEFAULT 'UTC',
    statistics_rebuild_pending  boolean NOT NULL DEFAULT false,
    server_private_key          bytea NOT NULL DEFAULT gen_random_bytes(32),
    telemetry_enabled           boolean NOT NULL DEFAULT true
);

CREATE UNIQUE INDEX platforms_singleton_unique
    ON platforms ((true));

COMMENT ON TABLE platforms IS '平台记录与平台级策略，首次安装时写入唯一一行';
COMMENT ON COLUMN platforms.server_id IS '服务器标识，首次安装或重置服务器标识时生成';
COMMENT ON COLUMN platforms.created_at IS '创建时间，即首次安装时间';
COMMENT ON COLUMN platforms.updated_at IS '更新时间';
COMMENT ON COLUMN platforms.registration_policy IS '注册策略：open 开放注册，invitation_only 仅限受邀邮箱注册';
COMMENT ON COLUMN platforms.workspace_creation_policy IS '工作区创建策略：any_account 所有账号可创建，platform_admin 仅平台管理员可创建';
COMMENT ON COLUMN platforms.statistics_time_zone IS '运营数据按日统计使用的 IANA 时区';
COMMENT ON COLUMN platforms.statistics_rebuild_pending IS '修改统计时区后等待按新时区全部重建运营数据';
COMMENT ON COLUMN platforms.server_private_key IS '服务器签名私钥（Ed25519 种子），用于向 control 证明服务器身份，首次安装或重置服务器标识时生成';
COMMENT ON COLUMN platforms.telemetry_enabled IS '是否向 control 上报运行指标';

-- +goose Down
DROP TABLE platforms;

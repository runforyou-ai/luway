-- +goose Up
-- 创建账号表。
CREATE TABLE accounts (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    email              text NOT NULL,
    email_verified_at  timestamptz,
    password_hash      text,
    display_name       text NOT NULL,
    locale             text NOT NULL DEFAULT 'zh-CN',
    time_zone          text NOT NULL DEFAULT 'Asia/Shanghai',
    status             text NOT NULL DEFAULT 'active',
    is_platform_admin  boolean NOT NULL DEFAULT false
);

CREATE UNIQUE INDEX accounts_email_unique
    ON accounts (lower(email));

COMMENT ON TABLE accounts IS '平台内的登录账号';
COMMENT ON COLUMN accounts.id IS '账号编号';
COMMENT ON COLUMN accounts.created_at IS '创建时间';
COMMENT ON COLUMN accounts.updated_at IS '更新时间';
COMMENT ON COLUMN accounts.email IS '登录与联系邮箱，全平台唯一';
COMMENT ON COLUMN accounts.email_verified_at IS '邮箱验证时间，为空表示未验证';
COMMENT ON COLUMN accounts.password_hash IS '本地登录密码哈希，为空表示没有本地密码';
COMMENT ON COLUMN accounts.display_name IS '账号名称，加入工作区时作为成员显示名称的默认值';
COMMENT ON COLUMN accounts.locale IS '界面语言';
COMMENT ON COLUMN accounts.time_zone IS '日期时间显示时区';
COMMENT ON COLUMN accounts.status IS '账号状态：active、inactive';
COMMENT ON COLUMN accounts.is_platform_admin IS '是否为平台管理员';

-- +goose Down
DROP TABLE accounts;

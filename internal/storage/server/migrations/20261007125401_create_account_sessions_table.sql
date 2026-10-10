-- +goose Up
-- 创建账号登录会话表。
CREATE TABLE account_sessions (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    account_id      uuid NOT NULL,
    token_hash      text NOT NULL,
    expires_at      timestamptz NOT NULL,
    push_app        varchar(255),
    push_platform   varchar(16),
    push_device_id  varchar(128)
);

CREATE INDEX account_sessions_account_idx
    ON account_sessions (account_id);

CREATE UNIQUE INDEX account_sessions_push_device_unique
    ON account_sessions (push_app, push_platform, push_device_id) WHERE (push_device_id IS NOT NULL);

CREATE UNIQUE INDEX account_sessions_token_hash_unique
    ON account_sessions (token_hash);

CREATE TRIGGER account_sessions_set_updated_at BEFORE UPDATE ON account_sessions FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE account_sessions IS '账号登录会话';
COMMENT ON COLUMN account_sessions.id IS '会话编号';
COMMENT ON COLUMN account_sessions.created_at IS '创建时间';
COMMENT ON COLUMN account_sessions.updated_at IS '更新时间';
COMMENT ON COLUMN account_sessions.account_id IS '账号编号';
COMMENT ON COLUMN account_sessions.token_hash IS '会话令牌摘要';
COMMENT ON COLUMN account_sessions.expires_at IS '过期时间';
COMMENT ON COLUMN account_sessions.push_app IS '推送应用标识，即注册设备的客户端应用标识';
COMMENT ON COLUMN account_sessions.push_platform IS '推送平台：android、ios';
COMMENT ON COLUMN account_sessions.push_device_id IS '推送服务分配给本设备的设备编号，未注册时为空';

-- +goose Down
DROP TABLE account_sessions;

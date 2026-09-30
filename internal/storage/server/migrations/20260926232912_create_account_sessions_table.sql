-- +goose Up
-- 创建账号登录会话表。
CREATE TABLE account_sessions (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    account_id  uuid NOT NULL,
    token_hash  text NOT NULL,
    expires_at  timestamptz NOT NULL
);

CREATE UNIQUE INDEX account_sessions_token_hash_unique
    ON account_sessions (token_hash);

COMMENT ON TABLE account_sessions IS '账号登录会话';
COMMENT ON COLUMN account_sessions.id IS '会话编号';
COMMENT ON COLUMN account_sessions.created_at IS '创建时间';
COMMENT ON COLUMN account_sessions.updated_at IS '更新时间';
COMMENT ON COLUMN account_sessions.account_id IS '账号编号';
COMMENT ON COLUMN account_sessions.token_hash IS '会话令牌摘要';
COMMENT ON COLUMN account_sessions.expires_at IS '过期时间';

-- +goose Down
DROP TABLE account_sessions;

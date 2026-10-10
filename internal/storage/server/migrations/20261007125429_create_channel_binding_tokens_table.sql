-- +goose Up
-- 创建渠道身份绑定令牌表。
CREATE TABLE channel_binding_tokens (
    id                   uuid PRIMARY KEY,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    workspace_id         uuid NOT NULL,
    channel_identity_id  uuid NOT NULL,
    token_hash           text NOT NULL,
    expires_at           timestamptz NOT NULL,
    delivered_at         timestamptz,
    used_at              timestamptz
);

CREATE UNIQUE INDEX channel_binding_tokens_token_hash_unique
    ON channel_binding_tokens (token_hash);

CREATE TRIGGER channel_binding_tokens_set_updated_at BEFORE UPDATE ON channel_binding_tokens FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE channel_binding_tokens IS '渠道身份绑定令牌，成员经绑定链接把服务员工的渠道身份绑定到自己';
COMMENT ON COLUMN channel_binding_tokens.id IS '令牌编号';
COMMENT ON COLUMN channel_binding_tokens.created_at IS '创建时间';
COMMENT ON COLUMN channel_binding_tokens.updated_at IS '更新时间';
COMMENT ON COLUMN channel_binding_tokens.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN channel_binding_tokens.channel_identity_id IS '待绑定的渠道身份编号';
COMMENT ON COLUMN channel_binding_tokens.token_hash IS '令牌原文的 SHA-256 摘要';
COMMENT ON COLUMN channel_binding_tokens.expires_at IS '过期时间';
COMMENT ON COLUMN channel_binding_tokens.delivered_at IS '平台确认收到绑定链接的时间，未确认送达时为空';
COMMENT ON COLUMN channel_binding_tokens.used_at IS '完成绑定的时间';

-- +goose Down
DROP TABLE channel_binding_tokens;

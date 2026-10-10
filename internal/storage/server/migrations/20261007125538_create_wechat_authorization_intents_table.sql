-- +goose Up
-- 创建公众号授权意图表。
CREATE TABLE wechat_authorization_intents (
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    channel_id     uuid PRIMARY KEY,
    workspace_id   uuid NOT NULL,
    state          text NOT NULL,
    pre_auth_code  text NOT NULL,
    expires_at     timestamptz NOT NULL,
    completed_at   timestamptz
);

CREATE UNIQUE INDEX wechat_authorization_intents_pre_auth_code_unique
    ON wechat_authorization_intents (pre_auth_code);

CREATE UNIQUE INDEX wechat_authorization_intents_state_unique
    ON wechat_authorization_intents (state);

CREATE TRIGGER wechat_authorization_intents_set_updated_at BEFORE UPDATE ON wechat_authorization_intents FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE wechat_authorization_intents IS '授权接入公众号渠道最近一次发起的授权，每个渠道一行，再次发起时覆盖';
COMMENT ON COLUMN wechat_authorization_intents.created_at IS '发起时间';
COMMENT ON COLUMN wechat_authorization_intents.updated_at IS '更新时间';
COMMENT ON COLUMN wechat_authorization_intents.channel_id IS '发起授权的公众号渠道';
COMMENT ON COLUMN wechat_authorization_intents.workspace_id IS '渠道所属企业';
COMMENT ON COLUMN wechat_authorization_intents.state IS '授权发起页与授权回跳地址中的随机标识';
COMMENT ON COLUMN wechat_authorization_intents.pre_auth_code IS '微信预授权码，用于关联授权成功通知';
COMMENT ON COLUMN wechat_authorization_intents.expires_at IS '预授权码到期时间，到期后须重新发起';
COMMENT ON COLUMN wechat_authorization_intents.completed_at IS '授权完成时间，完成后授权标识只用于确认授权结果';

-- +goose Down
DROP TABLE wechat_authorization_intents;

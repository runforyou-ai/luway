-- +goose Up
-- 创建公众号密钥接入凭据表。
CREATE TABLE wechat_channel_keys (
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    channel_id          uuid PRIMARY KEY,
    workspace_id        uuid NOT NULL,
    app_secret          text NOT NULL,
    token               text NOT NULL,
    encryption_mode     text NOT NULL,
    encoding_aes_key    text NOT NULL DEFAULT '',
    server_verified_at  timestamptz
);

CREATE TRIGGER wechat_channel_keys_set_updated_at BEFORE UPDATE ON wechat_channel_keys FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE wechat_channel_keys IS '密钥接入公众号渠道的凭据，公众号 AppID 为渠道的 provider_account_id';
COMMENT ON COLUMN wechat_channel_keys.created_at IS '创建时间';
COMMENT ON COLUMN wechat_channel_keys.updated_at IS '更新时间';
COMMENT ON COLUMN wechat_channel_keys.channel_id IS '公众号渠道';
COMMENT ON COLUMN wechat_channel_keys.workspace_id IS '渠道所属企业';
COMMENT ON COLUMN wechat_channel_keys.app_secret IS '公众号 AppSecret';
COMMENT ON COLUMN wechat_channel_keys.token IS '服务器配置的消息校验 Token';
COMMENT ON COLUMN wechat_channel_keys.encryption_mode IS '消息加密方式：plain 明文、safe 安全模式';
COMMENT ON COLUMN wechat_channel_keys.encoding_aes_key IS '安全模式的消息加解密密钥，明文模式为空';
COMMENT ON COLUMN wechat_channel_keys.server_verified_at IS '微信最近一次按当前 Token 验证服务器地址成功的时间，Token 变更后为空';

-- +goose Down
DROP TABLE wechat_channel_keys;

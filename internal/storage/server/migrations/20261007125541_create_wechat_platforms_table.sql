-- +goose Up
-- 创建微信第三方平台配置表。
CREATE TABLE wechat_platforms (
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    component_app_id           text PRIMARY KEY,
    component_app_secret       text NOT NULL,
    token                      text NOT NULL,
    encoding_aes_key           text NOT NULL,
    verify_ticket              text,
    verify_ticket_received_at  timestamptz
);

CREATE UNIQUE INDEX wechat_platforms_singleton_unique
    ON wechat_platforms ((true));

CREATE TRIGGER wechat_platforms_set_updated_at BEFORE UPDATE ON wechat_platforms FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE wechat_platforms IS '部署使用的微信第三方平台，至多一行；更换 Component AppID 时覆盖并清空验证票据';
COMMENT ON COLUMN wechat_platforms.created_at IS '首次保存时间';
COMMENT ON COLUMN wechat_platforms.updated_at IS '更新时间';
COMMENT ON COLUMN wechat_platforms.component_app_id IS '第三方平台 Component AppID';
COMMENT ON COLUMN wechat_platforms.component_app_secret IS '第三方平台 Component AppSecret';
COMMENT ON COLUMN wechat_platforms.token IS '消息校验 Token';
COMMENT ON COLUMN wechat_platforms.encoding_aes_key IS '消息加解密 EncodingAESKey';
COMMENT ON COLUMN wechat_platforms.verify_ticket IS '微信最近一次推送的 component_verify_ticket';
COMMENT ON COLUMN wechat_platforms.verify_ticket_received_at IS '最近一次收到验证票据的时间';

-- +goose Down
DROP TABLE wechat_platforms;

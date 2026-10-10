-- +goose Up
-- 创建企业微信智能机器人渠道设置表。
CREATE TABLE wecom_bot_channel_settings (
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    channel_id    uuid PRIMARY KEY,
    workspace_id  uuid NOT NULL,
    secret        text NOT NULL
);

CREATE TRIGGER wecom_bot_channel_settings_set_updated_at BEFORE UPDATE ON wecom_bot_channel_settings FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE wecom_bot_channel_settings IS '企业微信智能机器人渠道设置，机器人编号记在渠道的外部平台账号标识';
COMMENT ON COLUMN wecom_bot_channel_settings.created_at IS '创建时间';
COMMENT ON COLUMN wecom_bot_channel_settings.updated_at IS '更新时间';
COMMENT ON COLUMN wecom_bot_channel_settings.channel_id IS '渠道编号';
COMMENT ON COLUMN wecom_bot_channel_settings.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN wecom_bot_channel_settings.secret IS '机器人长连接密钥';

-- +goose Down
DROP TABLE wecom_bot_channel_settings;

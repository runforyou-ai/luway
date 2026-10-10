-- +goose Up
-- 创建 Telegram 渠道连接设置表。
CREATE TABLE telegram_channel_settings (
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    channel_id            uuid PRIMARY KEY,
    workspace_id          uuid NOT NULL,
    bot_token             text,
    bot_username          text,
    bot_display_name      text,
    webhook_base_url      text,
    webhook_secret        text,
    webhook_status        text,
    webhook_connected_at  timestamptz,
    connection_mode       text NOT NULL DEFAULT 'direct'
);

CREATE TRIGGER telegram_channel_settings_set_updated_at BEFORE UPDATE ON telegram_channel_settings FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE telegram_channel_settings IS 'Telegram 渠道连接设置';
COMMENT ON COLUMN telegram_channel_settings.created_at IS '创建时间';
COMMENT ON COLUMN telegram_channel_settings.updated_at IS '更新时间';
COMMENT ON COLUMN telegram_channel_settings.channel_id IS '渠道编号';
COMMENT ON COLUMN telegram_channel_settings.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN telegram_channel_settings.bot_token IS '机器人访问令牌';
COMMENT ON COLUMN telegram_channel_settings.bot_username IS 'Telegram 机器人用户名';
COMMENT ON COLUMN telegram_channel_settings.bot_display_name IS 'Telegram 机器人显示名称';
COMMENT ON COLUMN telegram_channel_settings.webhook_base_url IS 'Webhook 服务器基础地址';
COMMENT ON COLUMN telegram_channel_settings.webhook_secret IS '回调密钥：直连时为当前注册的 Webhook 密钥，网关转发时为业务系统转发请求使用的密钥';
COMMENT ON COLUMN telegram_channel_settings.webhook_status IS 'Webhook 连接状态';
COMMENT ON COLUMN telegram_channel_settings.webhook_connected_at IS '当前 Webhook 注册后首次成功回调的时间，重新注册或停用时清空';
COMMENT ON COLUMN telegram_channel_settings.connection_mode IS '接入方式：direct 由本服务注册 Webhook 直接接收，gateway 由业务系统接收后转发';

-- +goose Down
DROP TABLE telegram_channel_settings;

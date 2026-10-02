-- +goose Up
-- Telegram 渠道增加接入方式，渠道身份记录签名核验的企业用户编号。
ALTER TABLE telegram_channel_settings
    ADD COLUMN connection_mode text NOT NULL DEFAULT 'direct';

ALTER TABLE contact_channel_identities
    ADD COLUMN verified_user_id text;

COMMENT ON COLUMN telegram_channel_settings.connection_mode IS '接入方式：direct 由本服务注册 Webhook 直接接收，gateway 由业务系统接收后转发';
COMMENT ON COLUMN telegram_channel_settings.webhook_secret IS '回调密钥：直连时为当前注册的 Webhook 密钥，网关转发时为业务系统转发请求使用的密钥';
COMMENT ON COLUMN contact_channel_identities.verified_user_id IS '经签名身份核验的企业用户编号，与所属联系人的企业用户编号一致；未核验时为空';
COMMENT ON COLUMN contact_field_values.source IS '取值来源：member 客服填写，ai AI 根据对话填写，signed_identity 客户签名身份同步';
COMMENT ON COLUMN contact_tag_assignments.source IS '添加来源：member 客服添加，ai AI 根据对话添加，signed_identity 客户签名身份同步';

-- +goose Down
COMMENT ON COLUMN contact_tag_assignments.source IS '添加来源：member 客服添加，ai AI 根据对话添加，website 网站签名身份同步';
COMMENT ON COLUMN contact_field_values.source IS '取值来源：member 客服填写，ai AI 根据对话填写，website 网站签名身份同步';
COMMENT ON COLUMN telegram_channel_settings.webhook_secret IS 'Webhook 当前注册密钥';

ALTER TABLE contact_channel_identities
    DROP COLUMN verified_user_id;

ALTER TABLE telegram_channel_settings
    DROP COLUMN connection_mode;

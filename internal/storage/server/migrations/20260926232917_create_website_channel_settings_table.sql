-- +goose Up
-- 创建网站渠道聊天界面设置表。
CREATE TABLE website_channel_settings (
    created_at                      timestamptz NOT NULL DEFAULT now(),
    updated_at                      timestamptz NOT NULL DEFAULT now(),
    channel_id                      uuid PRIMARY KEY,
    organization_id                 uuid NOT NULL,
    chat_title                      text NOT NULL,
    greeting_message                text,
    theme_color                     text NOT NULL DEFAULT '#2563EB',
    allowed_embed_hosts             text[] NOT NULL DEFAULT '{}'::text[],
    home_enabled                    boolean NOT NULL DEFAULT false,
    help_enabled                    boolean NOT NULL DEFAULT false,
    home_welcome                    text,
    home_headline                   text,
    home_blocks                     jsonb NOT NULL DEFAULT '[{"type": "recent_conversation", "enabled": true}, {"type": "start_conversation", "enabled": true}, {"type": "links", "enabled": true}]'::jsonb,
    home_links                      jsonb NOT NULL DEFAULT '[]'::jsonb,
    attachments_enabled             boolean NOT NULL DEFAULT true,
    emoji_enabled                   boolean NOT NULL DEFAULT true,
    rating_enabled                  boolean NOT NULL DEFAULT true,
    multiple_conversations_enabled  boolean NOT NULL DEFAULT false
);

COMMENT ON TABLE website_channel_settings IS '网站渠道聊天界面设置';
COMMENT ON COLUMN website_channel_settings.created_at IS '创建时间';
COMMENT ON COLUMN website_channel_settings.updated_at IS '更新时间';
COMMENT ON COLUMN website_channel_settings.channel_id IS '渠道编号';
COMMENT ON COLUMN website_channel_settings.organization_id IS '所属工作区编号';
COMMENT ON COLUMN website_channel_settings.chat_title IS '聊天窗口标题';
COMMENT ON COLUMN website_channel_settings.greeting_message IS '访客欢迎语';
COMMENT ON COLUMN website_channel_settings.theme_color IS '界面主题色';
COMMENT ON COLUMN website_channel_settings.allowed_embed_hosts IS '允许嵌入聊天挂件的网站主机';
COMMENT ON COLUMN website_channel_settings.home_enabled IS '是否显示 Messenger 首页页签';
COMMENT ON COLUMN website_channel_settings.help_enabled IS '是否显示 Messenger 帮助页签，同时需要发布知识库';
COMMENT ON COLUMN website_channel_settings.home_welcome IS '首页问候语第一行，为空时使用默认文案';
COMMENT ON COLUMN website_channel_settings.home_headline IS '首页问候语第二行，为空时使用默认文案';
COMMENT ON COLUMN website_channel_settings.home_blocks IS '首页卡片的展示顺序与开关';
COMMENT ON COLUMN website_channel_settings.home_links IS 'Messenger 首页链接，按展示顺序保存标题与地址';
COMMENT ON COLUMN website_channel_settings.attachments_enabled IS '访客是否可以发送附件';
COMMENT ON COLUMN website_channel_settings.emoji_enabled IS '访客输入框是否显示表情';
COMMENT ON COLUMN website_channel_settings.rating_enabled IS '客服周期结束后是否邀请访客评价';
COMMENT ON COLUMN website_channel_settings.multiple_conversations_enabled IS '访客界面是否允许同一访客发起多个对话';

-- +goose Down
DROP TABLE website_channel_settings;

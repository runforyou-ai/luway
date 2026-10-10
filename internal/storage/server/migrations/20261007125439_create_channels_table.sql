-- +goose Up
-- 创建工作区渠道表。
CREATE TABLE channels (
    id                            uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at                    timestamptz NOT NULL DEFAULT now(),
    updated_at                    timestamptz NOT NULL DEFAULT now(),
    workspace_id                  uuid NOT NULL,
    created_by_user_id            uuid NOT NULL,
    type                          text NOT NULL,
    name                          text NOT NULL,
    description                   text,
    default_locale                text NOT NULL DEFAULT 'zh-CN',
    initial_routing_target_type   text NOT NULL DEFAULT 'public_queue',
    initial_routing_target_id     uuid,
    fallback_routing_target_type  text NOT NULL DEFAULT 'public_queue',
    fallback_routing_target_id    uuid,
    enabled                       boolean NOT NULL DEFAULT true,
    provider_account_id           text
);

CREATE UNIQUE INDEX channels_wechat_authorization_app_id_unique
    ON channels (provider_account_id) WHERE (type = 'wechat_official_account_authorization'::text);

CREATE UNIQUE INDEX channels_wechat_enabled_app_id_unique
    ON channels (provider_account_id) WHERE ((type = ANY (ARRAY['wechat_official_account_key'::text, 'wechat_official_account_authorization'::text])) AND enabled);

CREATE UNIQUE INDEX channels_wechat_key_app_id_unique
    ON channels (provider_account_id) WHERE (type = 'wechat_official_account_key'::text);

CREATE TRIGGER channels_set_updated_at BEFORE UPDATE ON channels FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE channels IS '工作区消息渠道';
COMMENT ON COLUMN channels.id IS '渠道编号';
COMMENT ON COLUMN channels.created_at IS '创建时间';
COMMENT ON COLUMN channels.updated_at IS '更新时间';
COMMENT ON COLUMN channels.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN channels.created_by_user_id IS '创建人编号';
COMMENT ON COLUMN channels.type IS '渠道类型';
COMMENT ON COLUMN channels.name IS '渠道名称';
COMMENT ON COLUMN channels.description IS '渠道描述';
COMMENT ON COLUMN channels.default_locale IS '默认接待语言';
COMMENT ON COLUMN channels.initial_routing_target_type IS '初始路由目标类型';
COMMENT ON COLUMN channels.initial_routing_target_id IS '初始路由团队或成员编号';
COMMENT ON COLUMN channels.fallback_routing_target_type IS '失败路由目标类型';
COMMENT ON COLUMN channels.fallback_routing_target_id IS '失败路由团队或成员编号';
COMMENT ON COLUMN channels.enabled IS '是否启用';
COMMENT ON COLUMN channels.provider_account_id IS '当前连接的外部平台账号标识，如 Telegram 机器人编号；未连接平台账号时为空';

-- +goose Down
DROP TABLE channels;

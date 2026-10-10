-- +goose Up
-- 创建渠道消息外部投递表。
CREATE TABLE channel_message_deliveries (
    id                         uuid PRIMARY KEY,
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    workspace_id               uuid NOT NULL,
    conversation_id            uuid NOT NULL,
    message_id                 uuid NOT NULL,
    channel_id                 uuid NOT NULL,
    channel_identity_id        uuid NOT NULL,
    provider_account_id        text NOT NULL,
    position                   bigint NOT NULL,
    status                     text NOT NULL DEFAULT 'pending',
    attempt                    integer NOT NULL DEFAULT 0,
    lease_worker               uuid,
    lease_expires_at           timestamptz,
    available_at               timestamptz NOT NULL DEFAULT now(),
    uncertain_until            timestamptz,
    last_error                 text NOT NULL DEFAULT '',
    sent_at                    timestamptz,
    reply_provider_message_id  text,
    reply_window_id            uuid,
    claimed_at                 timestamptz
);

CREATE UNIQUE INDEX channel_message_deliveries_active_identity_unique
    ON channel_message_deliveries (channel_id, channel_identity_id) WHERE (status = ANY (ARRAY['sending'::text, 'uncertain'::text]));

CREATE UNIQUE INDEX channel_message_deliveries_message_unique
    ON channel_message_deliveries (workspace_id, message_id);

CREATE UNIQUE INDEX channel_message_deliveries_position_unique
    ON channel_message_deliveries (channel_id, channel_identity_id, "position");

CREATE TRIGGER channel_message_deliveries_set_updated_at BEFORE UPDATE ON channel_message_deliveries FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE channel_message_deliveries IS '渠道消息外部投递';
COMMENT ON COLUMN channel_message_deliveries.id IS '投递编号';
COMMENT ON COLUMN channel_message_deliveries.created_at IS '创建时间';
COMMENT ON COLUMN channel_message_deliveries.updated_at IS '更新时间';
COMMENT ON COLUMN channel_message_deliveries.workspace_id IS '工作区编号';
COMMENT ON COLUMN channel_message_deliveries.conversation_id IS '会话编号';
COMMENT ON COLUMN channel_message_deliveries.message_id IS '本地消息编号';
COMMENT ON COLUMN channel_message_deliveries.channel_id IS '渠道编号';
COMMENT ON COLUMN channel_message_deliveries.channel_identity_id IS '接收渠道身份';
COMMENT ON COLUMN channel_message_deliveries.provider_account_id IS '入队时渠道连接的外部平台账号标识';
COMMENT ON COLUMN channel_message_deliveries.position IS '同一渠道身份内的投递顺序';
COMMENT ON COLUMN channel_message_deliveries.status IS '投递状态';
COMMENT ON COLUMN channel_message_deliveries.attempt IS '发送尝试次数';
COMMENT ON COLUMN channel_message_deliveries.lease_worker IS '发送认领标识';
COMMENT ON COLUMN channel_message_deliveries.lease_expires_at IS '认领过期时间';
COMMENT ON COLUMN channel_message_deliveries.available_at IS '允许发送时间';
COMMENT ON COLUMN channel_message_deliveries.uncertain_until IS '结果确认期限';
COMMENT ON COLUMN channel_message_deliveries.last_error IS '投递错误码';
COMMENT ON COLUMN channel_message_deliveries.sent_at IS '发送确认时间';
COMMENT ON COLUMN channel_message_deliveries.reply_provider_message_id IS '入队时确定的引用平台消息编号';
COMMENT ON COLUMN channel_message_deliveries.reply_window_id IS '投递未发送请求项预占额度的回复窗口编号，未预占时为空';
COMMENT ON COLUMN channel_message_deliveries.claimed_at IS '最近一次认领发送的时刻，取持有渠道身份锁后的数据库时刻';

-- +goose Down
DROP TABLE channel_message_deliveries;

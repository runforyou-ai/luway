-- +goose Up
-- 创建渠道消息身份与入站引用快照表。
CREATE TABLE channel_messages (
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    message_id                 uuid NOT NULL,
    workspace_id               uuid NOT NULL,
    conversation_id            uuid NOT NULL,
    channel_id                 uuid NOT NULL,
    provider_account_id        text NOT NULL,
    provider_conversation_id   text NOT NULL,
    provider_message_id        text NOT NULL,
    reply_provider_message_id  text,
    reply_body                 text NOT NULL DEFAULT '',
    reply_sender_name          text NOT NULL DEFAULT '',
    reply_sender_is_bot        boolean NOT NULL DEFAULT false,
    part                       integer NOT NULL DEFAULT 1,
    PRIMARY KEY (message_id, part)
);

CREATE UNIQUE INDEX channel_messages_provider_unique
    ON channel_messages (workspace_id, channel_id, provider_account_id, provider_conversation_id, provider_message_id);

CREATE INDEX channel_messages_reply_idx
    ON channel_messages (workspace_id, channel_id, provider_account_id, provider_conversation_id, reply_provider_message_id) WHERE (reply_provider_message_id IS NOT NULL);

CREATE TRIGGER channel_messages_set_updated_at BEFORE UPDATE ON channel_messages FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE channel_messages IS '渠道消息身份与入站引用快照';
COMMENT ON COLUMN channel_messages.created_at IS '创建时间';
COMMENT ON COLUMN channel_messages.updated_at IS '更新时间';
COMMENT ON COLUMN channel_messages.message_id IS '会话消息编号';
COMMENT ON COLUMN channel_messages.workspace_id IS '所属工作区';
COMMENT ON COLUMN channel_messages.conversation_id IS '所属会话';
COMMENT ON COLUMN channel_messages.channel_id IS '所属渠道';
COMMENT ON COLUMN channel_messages.provider_account_id IS '平台账号编号';
COMMENT ON COLUMN channel_messages.provider_conversation_id IS '平台聊天编号';
COMMENT ON COLUMN channel_messages.provider_message_id IS '平台消息编号';
COMMENT ON COLUMN channel_messages.reply_provider_message_id IS '引用的平台消息编号';
COMMENT ON COLUMN channel_messages.reply_body IS '引用原文快照';
COMMENT ON COLUMN channel_messages.reply_sender_name IS '引用发送者名称快照';
COMMENT ON COLUMN channel_messages.reply_sender_is_bot IS '引用发送者是否为机器人';
COMMENT ON COLUMN channel_messages.part IS '本地消息拆成多个平台消息时的分段序号，从 1 开始；入站消息为 1';

-- +goose Down
DROP TABLE channel_messages;

-- +goose Up
-- 创建渠道会话与联系人渠道身份的关系表。
CREATE TABLE channel_conversations (
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    conversation_id        uuid PRIMARY KEY,
    workspace_id           uuid NOT NULL,
    channel_identity_id    uuid NOT NULL,
    reply_language         text,
    contact_read_seq       bigint NOT NULL DEFAULT 0,
    contact_notified_seq   bigint NOT NULL DEFAULT 0,
    contact_notify_due_at  timestamptz
);

CREATE INDEX channel_conversations_channel_identity_idx
    ON channel_conversations (workspace_id, channel_identity_id, conversation_id);

CREATE TRIGGER channel_conversations_set_updated_at BEFORE UPDATE ON channel_conversations FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE channel_conversations IS '渠道会话与联系人渠道身份的关系';
COMMENT ON COLUMN channel_conversations.created_at IS '创建时间';
COMMENT ON COLUMN channel_conversations.updated_at IS '更新时间';
COMMENT ON COLUMN channel_conversations.conversation_id IS '渠道会话编号';
COMMENT ON COLUMN channel_conversations.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN channel_conversations.channel_identity_id IS '渠道身份编号';
COMMENT ON COLUMN channel_conversations.reply_language IS '客服锁定的对联系人回复语言，BCP 47 语言标签；为空时按联系人最近消息的语言回复';
COMMENT ON COLUMN channel_conversations.contact_read_seq IS '联系人在网站 Messenger 中已读到的最大消息序号，未读过时为 0';
COMMENT ON COLUMN channel_conversations.contact_notified_seq IS '已通过邮件通知联系人的最大消息序号，未通知过时为 0';
COMMENT ON COLUMN channel_conversations.contact_notify_due_at IS '检查未读真人回复并发送邮件通知的时间，由第一条未通知的真人回复起算；没有待通知回复时为空';

-- +goose Down
DROP TABLE channel_conversations;

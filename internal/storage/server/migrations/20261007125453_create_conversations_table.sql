-- +goose Up
-- 创建聊天会话表。
CREATE TABLE conversations (
    id                         uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    workspace_id               uuid NOT NULL,
    type                       text NOT NULL,
    status                     text NOT NULL DEFAULT 'active',
    title                      text,
    created_by_subject_id      uuid,
    last_message_id            uuid,
    last_message_at            timestamptz,
    description                text,
    image_file_id              uuid,
    last_message_seq           bigint NOT NULL DEFAULT 0,
    last_activity_at           timestamptz,
    version                    bigint NOT NULL DEFAULT 0,
    last_internal_activity_at  timestamptz
);

CREATE TRIGGER conversations_set_updated_at BEFORE UPDATE ON conversations FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE conversations IS '聊天会话';
COMMENT ON COLUMN conversations.id IS '会话编号';
COMMENT ON COLUMN conversations.created_at IS '创建时间';
COMMENT ON COLUMN conversations.updated_at IS '更新时间';
COMMENT ON COLUMN conversations.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN conversations.type IS '会话类型：direct、group、agent、channel、copilot';
COMMENT ON COLUMN conversations.status IS '会话生命周期状态：active、archived';
COMMENT ON COLUMN conversations.title IS '会话标题';
COMMENT ON COLUMN conversations.created_by_subject_id IS '创建聊天主体编号';
COMMENT ON COLUMN conversations.last_message_id IS '会话最后消息编号';
COMMENT ON COLUMN conversations.last_message_at IS '会话最后消息发生时间';
COMMENT ON COLUMN conversations.description IS '群聊描述';
COMMENT ON COLUMN conversations.image_file_id IS '群聊图片文件编号';
COMMENT ON COLUMN conversations.last_message_seq IS '会话已提交分配的最大消息序号，不随摘要重算回退';
COMMENT ON COLUMN conversations.last_activity_at IS '会话各方可见消息的最后追加活动时间';
COMMENT ON COLUMN conversations.version IS '会话可见变化版本，在会话锁内单调推进';
COMMENT ON COLUMN conversations.last_internal_activity_at IS '服务会话内部消息的最后追加活动时间';

-- +goose Down
DROP TABLE conversations;

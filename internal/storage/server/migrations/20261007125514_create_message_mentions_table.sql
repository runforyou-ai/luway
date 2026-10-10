-- +goose Up
-- 创建消息提醒关系表。
CREATE TABLE message_mentions (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    workspace_id  uuid NOT NULL,
    message_id    uuid NOT NULL,
    subject_id    uuid NOT NULL
);

CREATE UNIQUE INDEX message_mentions_workspace_message_subject_unique
    ON message_mentions (workspace_id, message_id, subject_id);

CREATE TRIGGER message_mentions_set_updated_at BEFORE UPDATE ON message_mentions FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE message_mentions IS '消息提醒关系';
COMMENT ON COLUMN message_mentions.id IS '消息提醒关系编号';
COMMENT ON COLUMN message_mentions.created_at IS '创建时间';
COMMENT ON COLUMN message_mentions.updated_at IS '更新时间';
COMMENT ON COLUMN message_mentions.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN message_mentions.message_id IS '消息编号';
COMMENT ON COLUMN message_mentions.subject_id IS '被提醒聊天主体编号';
COMMENT ON INDEX message_mentions_workspace_message_subject_unique IS '工作区消息内被提醒聊天主体唯一索引';

-- +goose Down
DROP TABLE message_mentions;

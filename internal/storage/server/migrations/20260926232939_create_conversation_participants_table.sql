-- +goose Up
-- 创建会话参与者关系表。
CREATE TABLE conversation_participants (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    organization_id  uuid NOT NULL,
    conversation_id  uuid NOT NULL,
    subject_id       uuid NOT NULL,
    role             text NOT NULL DEFAULT 'member',
    joined_at        timestamptz NOT NULL DEFAULT now(),
    left_at          timestamptz
);

CREATE UNIQUE INDEX conversation_participants_org_conversation_subject_unique
    ON conversation_participants (organization_id, conversation_id, subject_id);

COMMENT ON TABLE conversation_participants IS '会话参与者关系';
COMMENT ON COLUMN conversation_participants.id IS '会话参与者关系编号';
COMMENT ON COLUMN conversation_participants.created_at IS '创建时间';
COMMENT ON COLUMN conversation_participants.updated_at IS '更新时间';
COMMENT ON COLUMN conversation_participants.organization_id IS '所属工作区编号';
COMMENT ON COLUMN conversation_participants.conversation_id IS '会话编号';
COMMENT ON COLUMN conversation_participants.subject_id IS '聊天主体编号';
COMMENT ON COLUMN conversation_participants.role IS '会话参与角色：owner、member';
COMMENT ON COLUMN conversation_participants.joined_at IS '首次加入时间';
COMMENT ON COLUMN conversation_participants.left_at IS '离开时间';
COMMENT ON INDEX conversation_participants_org_conversation_subject_unique IS '工作区会话内聊天主体唯一索引';

-- +goose Down
DROP TABLE conversation_participants;

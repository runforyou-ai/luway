-- +goose Up
-- 创建内部单聊身份对表。
CREATE TABLE direct_conversations (
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    conversation_id     uuid PRIMARY KEY,
    organization_id     uuid NOT NULL,
    first_identity_id   uuid NOT NULL,
    second_identity_id  uuid NOT NULL
);

CREATE UNIQUE INDEX direct_conversations_organization_identity_pair_unique
    ON direct_conversations (organization_id, first_identity_id, second_identity_id);

COMMENT ON TABLE direct_conversations IS '内部单聊规范化身份对';
COMMENT ON COLUMN direct_conversations.created_at IS '创建时间';
COMMENT ON COLUMN direct_conversations.updated_at IS '更新时间';
COMMENT ON COLUMN direct_conversations.conversation_id IS '单聊会话编号';
COMMENT ON COLUMN direct_conversations.organization_id IS '所属工作区编号';
COMMENT ON COLUMN direct_conversations.first_identity_id IS '规范化排序后的第一个工作区身份编号';
COMMENT ON COLUMN direct_conversations.second_identity_id IS '规范化排序后的第二个工作区身份编号';
COMMENT ON INDEX direct_conversations_organization_identity_pair_unique IS '工作区内规范化单聊身份对唯一索引';

-- +goose Down
DROP TABLE direct_conversations;

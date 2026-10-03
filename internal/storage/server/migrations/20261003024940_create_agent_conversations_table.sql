-- +goose Up
-- 创建 AI 聊天业务归属表。
CREATE TABLE agent_conversations (
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    conversation_id       uuid PRIMARY KEY,
    organization_id       uuid NOT NULL,
    user_identity_id      uuid NOT NULL,
    agent_identity_id     uuid NOT NULL,
    memory_extracted_seq  bigint NOT NULL DEFAULT 0
);

COMMENT ON TABLE agent_conversations IS 'AI 聊天业务归属';
COMMENT ON COLUMN agent_conversations.created_at IS '创建时间';
COMMENT ON COLUMN agent_conversations.updated_at IS '更新时间';
COMMENT ON COLUMN agent_conversations.conversation_id IS '会话编号';
COMMENT ON COLUMN agent_conversations.organization_id IS '所属工作区编号';
COMMENT ON COLUMN agent_conversations.user_identity_id IS '所属成员身份编号';
COMMENT ON COLUMN agent_conversations.agent_identity_id IS '目标 Agent 身份编号';
COMMENT ON COLUMN agent_conversations.memory_extracted_seq IS 'AI 员工记忆已提取到的会话消息序号';

-- +goose Down
DROP TABLE agent_conversations;

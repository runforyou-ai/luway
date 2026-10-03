-- +goose Up
-- 创建 AI 员工记忆表。
CREATE TABLE agent_memories (
    id               uuid PRIMARY KEY,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    organization_id  uuid NOT NULL,
    agent_id         uuid NOT NULL,
    path             text NOT NULL,
    name             text NOT NULL,
    description      text NOT NULL,
    body             text NOT NULL
);

CREATE UNIQUE INDEX agent_memories_agent_id_path_key
    ON agent_memories (agent_id, path);

COMMENT ON TABLE agent_memories IS '仅服务负责人本人的 AI 员工的长期记忆条目';
COMMENT ON COLUMN agent_memories.id IS '记忆编号';
COMMENT ON COLUMN agent_memories.created_at IS '创建时间';
COMMENT ON COLUMN agent_memories.updated_at IS '更新时间';
COMMENT ON COLUMN agent_memories.organization_id IS '所属工作区编号';
COMMENT ON COLUMN agent_memories.agent_id IS '所属 AI 员工编号';
COMMENT ON COLUMN agent_memories.path IS '记忆目录下的文件名，同一 AI 员工内唯一';
COMMENT ON COLUMN agent_memories.name IS '记忆名称';
COMMENT ON COLUMN agent_memories.description IS '一句话说明，用于召回时挑选';
COMMENT ON COLUMN agent_memories.body IS '记忆正文';

-- +goose Down
DROP TABLE agent_memories;

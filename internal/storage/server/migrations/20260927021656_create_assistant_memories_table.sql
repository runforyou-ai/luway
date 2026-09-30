-- +goose Up
CREATE TABLE assistant_memories (
    id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    organization_id uuid NOT NULL,
    agent_id uuid NOT NULL,
    path text NOT NULL,
    name text NOT NULL,
    description text NOT NULL,
    body text NOT NULL
);

CREATE UNIQUE INDEX assistant_memories_agent_id_path_key ON assistant_memories (agent_id, path);

COMMENT ON TABLE assistant_memories IS '助理的长期记忆条目';
COMMENT ON COLUMN assistant_memories.id IS '记忆编号';
COMMENT ON COLUMN assistant_memories.created_at IS '创建时间';
COMMENT ON COLUMN assistant_memories.updated_at IS '更新时间';
COMMENT ON COLUMN assistant_memories.organization_id IS '所属工作区编号';
COMMENT ON COLUMN assistant_memories.agent_id IS '所属助理编号';
COMMENT ON COLUMN assistant_memories.path IS '记忆目录下的文件名，同一助理内唯一';
COMMENT ON COLUMN assistant_memories.name IS '记忆名称';
COMMENT ON COLUMN assistant_memories.description IS '一句话说明，用于召回时挑选';
COMMENT ON COLUMN assistant_memories.body IS '记忆正文';

-- +goose Down
DROP TABLE assistant_memories;

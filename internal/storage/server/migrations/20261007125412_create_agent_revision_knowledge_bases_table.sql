-- +goose Up
-- 创建 AI 员工配置版本绑定的知识库表，随配置版本一并写入，写入后保持不变。
CREATE TABLE agent_revision_knowledge_bases (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    workspace_id       uuid NOT NULL,
    agent_id           uuid NOT NULL,
    revision_id        uuid NOT NULL,
    knowledge_base_id  uuid NOT NULL
);

CREATE INDEX agent_revision_knowledge_bases_knowledge_base_idx
    ON agent_revision_knowledge_bases (workspace_id, knowledge_base_id);

CREATE UNIQUE INDEX agent_revision_knowledge_bases_revision_knowledge_base_unique
    ON agent_revision_knowledge_bases (workspace_id, revision_id, knowledge_base_id);

CREATE TRIGGER agent_revision_knowledge_bases_set_updated_at BEFORE UPDATE ON agent_revision_knowledge_bases FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE agent_revision_knowledge_bases IS 'AI 员工配置版本绑定的知识库';
COMMENT ON COLUMN agent_revision_knowledge_bases.id IS '引用编号';
COMMENT ON COLUMN agent_revision_knowledge_bases.created_at IS '创建时间';
COMMENT ON COLUMN agent_revision_knowledge_bases.updated_at IS '更新时间';
COMMENT ON COLUMN agent_revision_knowledge_bases.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN agent_revision_knowledge_bases.agent_id IS 'AI 员工编号';
COMMENT ON COLUMN agent_revision_knowledge_bases.revision_id IS '配置版本编号';
COMMENT ON COLUMN agent_revision_knowledge_bases.knowledge_base_id IS '知识库编号';

-- +goose Down
DROP TABLE agent_revision_knowledge_bases;

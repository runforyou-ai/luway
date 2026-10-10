-- +goose Up
-- 创建 AI 员工配置版本表。
CREATE TABLE agent_revisions (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    workspace_id        uuid NOT NULL,
    agent_id            uuid NOT NULL,
    execution_mode      text NOT NULL,
    schema_version      integer NOT NULL,
    configuration       jsonb NOT NULL,
    created_by_user_id  uuid NOT NULL,
    model_id            uuid
);

CREATE TRIGGER agent_revisions_set_updated_at BEFORE UPDATE ON agent_revisions FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE agent_revisions IS 'AI 员工不可变配置版本';
COMMENT ON COLUMN agent_revisions.id IS '配置版本编号';
COMMENT ON COLUMN agent_revisions.created_at IS '创建时间';
COMMENT ON COLUMN agent_revisions.updated_at IS '更新时间';
COMMENT ON COLUMN agent_revisions.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN agent_revisions.agent_id IS 'AI 员工编号';
COMMENT ON COLUMN agent_revisions.execution_mode IS '执行方式';
COMMENT ON COLUMN agent_revisions.schema_version IS '配置结构版本';
COMMENT ON COLUMN agent_revisions.configuration IS '非敏感执行配置快照，不含模型引用';
COMMENT ON COLUMN agent_revisions.created_by_user_id IS '创建用户编号';
COMMENT ON COLUMN agent_revisions.model_id IS '托管执行使用的对话模型编号，本机 Agent 执行为空';

-- +goose Down
DROP TABLE agent_revisions;

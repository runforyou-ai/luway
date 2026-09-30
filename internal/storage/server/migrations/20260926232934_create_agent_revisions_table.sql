-- +goose Up
-- 创建 AI 员工配置版本表。
CREATE TABLE agent_revisions (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    organization_id     uuid NOT NULL,
    agent_id            uuid NOT NULL,
    execution_mode      text NOT NULL,
    schema_version      integer NOT NULL,
    configuration       jsonb NOT NULL,
    created_by_user_id  uuid NOT NULL
);

COMMENT ON TABLE agent_revisions IS 'AI 员工不可变配置版本';
COMMENT ON COLUMN agent_revisions.id IS '配置版本编号';
COMMENT ON COLUMN agent_revisions.created_at IS '创建时间';
COMMENT ON COLUMN agent_revisions.updated_at IS '更新时间';
COMMENT ON COLUMN agent_revisions.organization_id IS '所属工作区编号';
COMMENT ON COLUMN agent_revisions.agent_id IS 'AI 员工编号';
COMMENT ON COLUMN agent_revisions.execution_mode IS '执行方式';
COMMENT ON COLUMN agent_revisions.schema_version IS '配置结构版本';
COMMENT ON COLUMN agent_revisions.configuration IS '非敏感执行配置快照';
COMMENT ON COLUMN agent_revisions.created_by_user_id IS '创建用户编号';

-- +goose Down
DROP TABLE agent_revisions;

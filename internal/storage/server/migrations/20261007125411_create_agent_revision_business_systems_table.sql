-- +goose Up
-- 创建 AI 员工配置版本授权的业务系统表，随配置版本一并写入，写入后保持不变。
CREATE TABLE agent_revision_business_systems (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    workspace_id        uuid NOT NULL,
    agent_id            uuid NOT NULL,
    revision_id         uuid NOT NULL,
    business_system_id  uuid NOT NULL
);

CREATE INDEX agent_revision_business_systems_business_system_idx
    ON agent_revision_business_systems (workspace_id, business_system_id);

CREATE UNIQUE INDEX agent_revision_business_systems_revision_system_unique
    ON agent_revision_business_systems (workspace_id, revision_id, business_system_id);

CREATE TRIGGER agent_revision_business_systems_set_updated_at BEFORE UPDATE ON agent_revision_business_systems FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE agent_revision_business_systems IS 'AI 员工配置版本授权的业务系统';
COMMENT ON COLUMN agent_revision_business_systems.id IS '引用编号';
COMMENT ON COLUMN agent_revision_business_systems.created_at IS '创建时间';
COMMENT ON COLUMN agent_revision_business_systems.updated_at IS '更新时间';
COMMENT ON COLUMN agent_revision_business_systems.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN agent_revision_business_systems.agent_id IS 'AI 员工编号';
COMMENT ON COLUMN agent_revision_business_systems.revision_id IS '配置版本编号';
COMMENT ON COLUMN agent_revision_business_systems.business_system_id IS '业务系统编号';

-- +goose Down
DROP TABLE agent_revision_business_systems;

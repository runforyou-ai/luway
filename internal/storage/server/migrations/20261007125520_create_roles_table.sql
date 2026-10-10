-- +goose Up
-- 创建工作区角色表。
CREATE TABLE roles (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    workspace_id  uuid NOT NULL,
    kind          text NOT NULL,
    name          text NOT NULL DEFAULT '',
    description   text NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX roles_workspace_builtin_kind_unique
    ON roles (workspace_id, kind) WHERE (kind <> 'custom'::text);

CREATE UNIQUE INDEX roles_workspace_custom_name_unique
    ON roles (workspace_id, lower(name)) WHERE (kind = 'custom'::text);

CREATE TRIGGER roles_set_updated_at BEFORE UPDATE ON roles FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE roles IS '工作区角色';
COMMENT ON COLUMN roles.id IS '角色编号';
COMMENT ON COLUMN roles.created_at IS '创建时间';
COMMENT ON COLUMN roles.updated_at IS '更新时间';
COMMENT ON COLUMN roles.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN roles.kind IS '内置角色类型或自定义角色标识';
COMMENT ON COLUMN roles.name IS '自定义角色名称';
COMMENT ON COLUMN roles.description IS '自定义角色说明';

-- +goose Down
DROP TABLE roles;

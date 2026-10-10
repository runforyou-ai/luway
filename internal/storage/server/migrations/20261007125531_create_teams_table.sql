-- +goose Up
-- 创建工作区团队表，关联关系由 Action 维护。
CREATE TABLE teams (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    workspace_id        uuid NOT NULL,
    name                text NOT NULL,
    description         text NOT NULL DEFAULT '',
    created_by_user_id  uuid NOT NULL
);

CREATE UNIQUE INDEX teams_workspace_name_unique
    ON teams (workspace_id, lower(name));

CREATE TRIGGER teams_set_updated_at BEFORE UPDATE ON teams FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE teams IS '工作区团队';
COMMENT ON COLUMN teams.id IS '团队编号';
COMMENT ON COLUMN teams.created_at IS '创建时间';
COMMENT ON COLUMN teams.updated_at IS '更新时间';
COMMENT ON COLUMN teams.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN teams.name IS '团队名称';
COMMENT ON COLUMN teams.description IS '团队简介';
COMMENT ON COLUMN teams.created_by_user_id IS '创建用户编号';

-- +goose Down
DROP TABLE teams;

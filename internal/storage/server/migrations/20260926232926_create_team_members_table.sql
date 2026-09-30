-- +goose Up
-- 创建团队成员关系表，关联关系由 Action 维护。
CREATE TABLE team_members (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    organization_id     uuid NOT NULL,
    team_id             uuid NOT NULL,
    identity_id         uuid NOT NULL,
    created_by_user_id  uuid NOT NULL
);

CREATE UNIQUE INDEX team_members_organization_team_identity_unique
    ON team_members (organization_id, team_id, identity_id);

COMMENT ON TABLE team_members IS '团队成员关系';
COMMENT ON COLUMN team_members.id IS '关系编号';
COMMENT ON COLUMN team_members.created_at IS '创建时间';
COMMENT ON COLUMN team_members.updated_at IS '更新时间';
COMMENT ON COLUMN team_members.organization_id IS '所属工作区编号';
COMMENT ON COLUMN team_members.team_id IS '所属团队编号';
COMMENT ON COLUMN team_members.identity_id IS '工作区身份编号';
COMMENT ON COLUMN team_members.created_by_user_id IS '创建用户编号';

-- +goose Down
DROP TABLE team_members;

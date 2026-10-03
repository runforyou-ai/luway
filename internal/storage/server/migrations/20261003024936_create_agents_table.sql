-- +goose Up
-- 创建 AI 员工表。
CREATE TABLE agents (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    identity_id          uuid NOT NULL,
    organization_id      uuid NOT NULL,
    status               text NOT NULL DEFAULT 'active',
    active_revision_id   uuid NOT NULL,
    device_id            uuid,
    paused_at            timestamptz,
    service_audiences    text[] NOT NULL DEFAULT '{}'::text[],
    handoff_team_id      uuid,
    responsible_user_id  uuid
);

CREATE UNIQUE INDEX agents_organization_active_revision_unique
    ON agents (organization_id, active_revision_id);

CREATE UNIQUE INDEX agents_organization_identity_unique
    ON agents (organization_id, identity_id);

COMMENT ON TABLE agents IS 'AI 员工';
COMMENT ON COLUMN agents.id IS 'AI 员工编号';
COMMENT ON COLUMN agents.created_at IS '创建时间';
COMMENT ON COLUMN agents.updated_at IS '更新时间';
COMMENT ON COLUMN agents.identity_id IS '工作区身份编号';
COMMENT ON COLUMN agents.organization_id IS '所属工作区编号';
COMMENT ON COLUMN agents.status IS '账号状态';
COMMENT ON COLUMN agents.active_revision_id IS '当前配置版本编号';
COMMENT ON COLUMN agents.device_id IS '仅服务负责人本人的 AI 员工绑定的电脑编号，其他 AI 员工为空';
COMMENT ON COLUMN agents.paused_at IS '负责人暂停仅服务本人的 AI 员工的时间，非空表示暂停';
COMMENT ON COLUMN agents.service_audiences IS 'AI 员工的服务对象：customer 客户、employee 本工作区成员，或单独的 personal 仅负责人本人';
COMMENT ON COLUMN agents.handoff_team_id IS '单聊转人工的团队编号，为空时进入公共队列';
COMMENT ON COLUMN agents.responsible_user_id IS 'AI 员工负责人编号；仅服务负责人本人时必填，其他 AI 员工为空表示未指定';

-- +goose Down
DROP TABLE agents;

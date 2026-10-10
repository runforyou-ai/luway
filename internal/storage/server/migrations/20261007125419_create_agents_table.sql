-- +goose Up
-- 创建 AI 员工表。
CREATE TABLE agents (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    identity_id          uuid NOT NULL,
    workspace_id         uuid NOT NULL,
    status               text NOT NULL DEFAULT 'active',
    active_revision_id   uuid NOT NULL,
    computer_id          uuid,
    paused_at            timestamptz,
    service_audiences    text[] NOT NULL DEFAULT '{}'::text[],
    handoff_team_id      uuid,
    responsible_user_id  uuid,
    computer_grant       jsonb,
    local_agents         jsonb NOT NULL DEFAULT '[]'::jsonb
);

CREATE UNIQUE INDEX agents_workspace_active_revision_unique
    ON agents (workspace_id, active_revision_id);

CREATE UNIQUE INDEX agents_workspace_identity_unique
    ON agents (workspace_id, identity_id);

CREATE TRIGGER agents_set_updated_at BEFORE UPDATE ON agents FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE agents IS 'AI 员工';
COMMENT ON COLUMN agents.id IS 'AI 员工编号';
COMMENT ON COLUMN agents.created_at IS '创建时间';
COMMENT ON COLUMN agents.updated_at IS '更新时间';
COMMENT ON COLUMN agents.identity_id IS '工作区身份编号';
COMMENT ON COLUMN agents.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN agents.status IS '账号状态';
COMMENT ON COLUMN agents.active_revision_id IS '当前配置版本编号';
COMMENT ON COLUMN agents.computer_id IS 'AI 员工使用的电脑编号：个人 AI 员工为负责人本人的个人电脑，服务型 AI 员工为工作区电脑，未使用电脑时为空';
COMMENT ON COLUMN agents.paused_at IS '负责人暂停仅服务本人的 AI 员工的时间，非空表示暂停';
COMMENT ON COLUMN agents.service_audiences IS 'AI 员工的服务对象：customer 客户、employee 本工作区成员，或单独的 personal 仅负责人本人';
COMMENT ON COLUMN agents.handoff_team_id IS '单聊转人工的团队编号，为空时进入公共队列';
COMMENT ON COLUMN agents.responsible_user_id IS 'AI 员工负责人编号；仅服务负责人本人时必填，其他 AI 员工为空表示未指定';
COMMENT ON COLUMN agents.computer_grant IS '服务型 AI 员工对所用工作区电脑的授权：可执行的最高级别 maxLevel 与 L2 是否需要确认 confirmL2；个人 AI 员工与未使用电脑时为空';
COMMENT ON COLUMN agents.local_agents IS 'AI 员工启用的本机 Agent 名称';

-- +goose Down
DROP TABLE agents;

-- +goose Up
-- 创建 Agent 执行范围输入队列表。
CREATE TABLE agent_lanes (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    workspace_id       uuid NOT NULL,
    conversation_id    uuid NOT NULL,
    agent_identity_id  uuid NOT NULL,
    scope_kind         text NOT NULL,
    scope_id           uuid NOT NULL,
    desired_seq        bigint NOT NULL DEFAULT 0,
    processed_seq      bigint NOT NULL DEFAULT 0
);

CREATE INDEX agent_lanes_conversation_idx
    ON agent_lanes (workspace_id, conversation_id);

CREATE UNIQUE INDEX agent_lanes_scope_agent_unique
    ON agent_lanes (workspace_id, scope_kind, scope_id, agent_identity_id);

CREATE TRIGGER agent_lanes_set_updated_at BEFORE UPDATE ON agent_lanes FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE agent_lanes IS 'Agent 在一个执行范围内的输入队列与水位';
COMMENT ON COLUMN agent_lanes.id IS '输入队列编号';
COMMENT ON COLUMN agent_lanes.created_at IS '创建时间';
COMMENT ON COLUMN agent_lanes.updated_at IS '更新时间';
COMMENT ON COLUMN agent_lanes.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN agent_lanes.conversation_id IS '执行范围所属会话编号';
COMMENT ON COLUMN agent_lanes.agent_identity_id IS '目标 Agent 工作区身份编号';
COMMENT ON COLUMN agent_lanes.scope_kind IS '执行范围类型：conversation、service_session';
COMMENT ON COLUMN agent_lanes.scope_id IS '执行范围编号，取会话编号或客服周期编号';
COMMENT ON COLUMN agent_lanes.desired_seq IS '已经持久化的最新输入序号';
COMMENT ON COLUMN agent_lanes.processed_seq IS '已经得到明确终态的输入序号';

-- +goose Down
DROP TABLE agent_lanes;

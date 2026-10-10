-- +goose Up
-- 创建本机 Agent 会话表。
CREATE TABLE local_agent_sessions (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    workspace_id     uuid NOT NULL,
    conversation_id  uuid NOT NULL,
    agent_id         uuid NOT NULL,
    computer_id      uuid NOT NULL,
    local_agent      text NOT NULL,
    session_id       text,
    status           text NOT NULL
);

CREATE UNIQUE INDEX local_agent_sessions_active_unique
    ON local_agent_sessions (workspace_id, conversation_id, agent_id, local_agent) WHERE (status = 'active'::text);

CREATE TRIGGER local_agent_sessions_set_updated_at BEFORE UPDATE ON local_agent_sessions FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE local_agent_sessions IS 'AI 员工在会话中委派给电脑上本机 Agent 的会话，跨多轮续接，归会话所有';
COMMENT ON COLUMN local_agent_sessions.id IS '本机 Agent 会话编号';
COMMENT ON COLUMN local_agent_sessions.created_at IS '创建时间';
COMMENT ON COLUMN local_agent_sessions.updated_at IS '更新时间';
COMMENT ON COLUMN local_agent_sessions.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN local_agent_sessions.conversation_id IS '发起委派的会话编号';
COMMENT ON COLUMN local_agent_sessions.agent_id IS '发起委派的 AI 员工编号';
COMMENT ON COLUMN local_agent_sessions.computer_id IS '运行本机 Agent 的电脑编号';
COMMENT ON COLUMN local_agent_sessions.local_agent IS '电脑上报的本机 Agent 名称';
COMMENT ON COLUMN local_agent_sessions.session_id IS '本机 Agent 返回的 ACP 会话编号，首轮完成后由执行器上报';
COMMENT ON COLUMN local_agent_sessions.status IS '会话状态：active 执行器持有会话，released 已释放';

-- +goose Down
DROP TABLE local_agent_sessions;

-- +goose Up
-- 创建服务会话 Copilot 线程归属表。
CREATE TABLE service_copilot_threads (
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    conversation_id         uuid PRIMARY KEY,
    workspace_id            uuid NOT NULL,
    served_conversation_id  uuid NOT NULL,
    agent_identity_id       uuid NOT NULL,
    created_by_identity_id  uuid NOT NULL
);

CREATE INDEX service_copilot_threads_served_idx
    ON service_copilot_threads (workspace_id, served_conversation_id);

CREATE TRIGGER service_copilot_threads_set_updated_at BEFORE UPDATE ON service_copilot_threads FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE service_copilot_threads IS '服务会话 Copilot 线程归属';
COMMENT ON COLUMN service_copilot_threads.created_at IS '创建时间';
COMMENT ON COLUMN service_copilot_threads.updated_at IS '更新时间';
COMMENT ON COLUMN service_copilot_threads.conversation_id IS 'Copilot 线程会话编号';
COMMENT ON COLUMN service_copilot_threads.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN service_copilot_threads.served_conversation_id IS '承载所属服务会话的会话编号';
COMMENT ON COLUMN service_copilot_threads.agent_identity_id IS '回答问题的 AI 员工身份编号';
COMMENT ON COLUMN service_copilot_threads.created_by_identity_id IS '创建线程的成员身份编号';

-- +goose Down
DROP TABLE service_copilot_threads;

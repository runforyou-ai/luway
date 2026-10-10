-- +goose Up
-- 创建 Agent 运行的有序中间内容表。
CREATE TABLE agent_run_blocks (
    id             uuid PRIMARY KEY,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    workspace_id   uuid NOT NULL,
    agent_run_id   uuid NOT NULL,
    position       bigint NOT NULL,
    model_call_id  uuid NOT NULL,
    kind           text NOT NULL,
    content        text,
    tool_call_id   uuid
);

CREATE UNIQUE INDEX agent_run_blocks_run_position_unique
    ON agent_run_blocks (agent_run_id, "position");

CREATE TRIGGER agent_run_blocks_set_updated_at BEFORE UPDATE ON agent_run_blocks FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE agent_run_blocks IS 'Agent 运行的有序中间内容，每次模型调用定稿后写入';
COMMENT ON COLUMN agent_run_blocks.id IS '运行时生成的内容块编号';
COMMENT ON COLUMN agent_run_blocks.created_at IS '创建时间';
COMMENT ON COLUMN agent_run_blocks.updated_at IS '更新时间';
COMMENT ON COLUMN agent_run_blocks.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN agent_run_blocks.agent_run_id IS '所属 Agent 运行编号';
COMMENT ON COLUMN agent_run_blocks.position IS '块在所属运行内的顺序位置';
COMMENT ON COLUMN agent_run_blocks.model_call_id IS '所属模型调用编号';
COMMENT ON COLUMN agent_run_blocks.kind IS '内容类型：thinking、content、tool_call';
COMMENT ON COLUMN agent_run_blocks.content IS '思考或正文的完整文本，工具调用块为空';
COMMENT ON COLUMN agent_run_blocks.tool_call_id IS '工具调用块对应的工具调用编号，其他块为空';

-- +goose Down
DROP TABLE agent_run_blocks;

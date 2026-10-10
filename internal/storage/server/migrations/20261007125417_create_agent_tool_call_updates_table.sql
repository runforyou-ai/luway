-- +goose Up
-- 创建工具调用过程更新表。
CREATE TABLE agent_tool_call_updates (
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    tool_call_id  uuid NOT NULL,
    seq           integer NOT NULL,
    workspace_id  uuid NOT NULL,
    content       jsonb NOT NULL,
    PRIMARY KEY (tool_call_id, seq)
);

CREATE TRIGGER agent_tool_call_updates_set_updated_at BEFORE UPDATE ON agent_tool_call_updates FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE agent_tool_call_updates IS '电脑执行工具调用期间按顺序上报的过程更新';
COMMENT ON COLUMN agent_tool_call_updates.created_at IS '写入时间';
COMMENT ON COLUMN agent_tool_call_updates.updated_at IS '更新时间';
COMMENT ON COLUMN agent_tool_call_updates.tool_call_id IS '所属工具调用编号';
COMMENT ON COLUMN agent_tool_call_updates.seq IS '执行器分配的更新序号，从 1 开始连续递增，重复上报同一序号只保留首次写入';
COMMENT ON COLUMN agent_tool_call_updates.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN agent_tool_call_updates.content IS '过程更新：回复片段、思考片段、工具调用及其文件差异或任务清单';

-- +goose Down
DROP TABLE agent_tool_call_updates;

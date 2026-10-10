-- +goose Up
-- 暂停运行等待确认的工具调用保存处理人的决定，运行恢复后据此执行或拒绝。
ALTER TABLE agent_tool_calls
    ADD COLUMN decision jsonb;

COMMENT ON COLUMN agent_tool_calls.decision IS '暂停运行等待确认的调用的决定：是否批准、拒绝理由与改用的参数，运行恢复后据此在运行内执行或拒绝；提交给处理人的调用为空';

-- +goose Down
ALTER TABLE agent_tool_calls
    DROP COLUMN decision;

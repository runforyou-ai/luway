-- +goose Up
-- 工具调用记录保存运行时的修订号、交接方式、交接载荷、注解、结果媒体与完成载荷，运行记录保存当前生效的完成。
ALTER TABLE agent_tool_calls
    ADD COLUMN rev        bigint NOT NULL DEFAULT 0,
    ADD COLUMN handover   text,
    ADD COLUMN payload    jsonb,
    ADD COLUMN notes      jsonb,
    ADD COLUMN media      jsonb,
    ADD COLUMN completion jsonb;

ALTER TABLE agent_runs
    ADD COLUMN completion jsonb;

COMMENT ON COLUMN agent_tool_calls.rev IS '运行时对调用记录的修订号，写入时只保留修订号更大的快照';
COMMENT ON COLUMN agent_tool_calls.handover IS '调用交给运行外推进的方式：await 等待外部结果、detached 交出后运行继续、submitted 提交确认或审批，由运行时推进的调用为空';
COMMENT ON COLUMN agent_tool_calls.payload IS '调用交出或提交时运行时给出的载荷';
COMMENT ON COLUMN agent_tool_calls.notes IS '工具规格、依据检查与扩展写入的调用注解';
COMMENT ON COLUMN agent_tool_calls.media IS '调用结果附带的媒体引用，按顺序排列';
COMMENT ON COLUMN agent_tool_calls.completion IS '结束运行的调用给出的完成载荷';
COMMENT ON COLUMN agent_runs.completion IS '运行当前生效的完成：结束方式与载荷，没有时为空';

-- +goose Down
ALTER TABLE agent_runs
    DROP COLUMN completion;

ALTER TABLE agent_tool_calls
    DROP COLUMN completion,
    DROP COLUMN media,
    DROP COLUMN notes,
    DROP COLUMN payload,
    DROP COLUMN handover,
    DROP COLUMN rev;

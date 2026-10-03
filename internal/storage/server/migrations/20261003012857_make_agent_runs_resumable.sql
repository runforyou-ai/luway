-- +goose Up
-- 运行保存恢复状态并可挂起等待，中间内容逐步写入并引用工具调用记录。
ALTER TABLE agent_runs ADD COLUMN state bytea;
ALTER TABLE agent_runs ADD COLUMN task_run_id uuid;

COMMENT ON COLUMN agent_runs.task_run_id IS '执行或即将执行运行的任务编号，同一运行同一时刻只由该任务执行';
COMMENT ON COLUMN agent_runs.state IS '运行恢复状态：模型上下文、输入边界与执行期计数，在每次模型输出定稿与每批工具结果返回后写入';
COMMENT ON COLUMN agent_runs.status IS '运行状态：queued、running、waiting 挂起等待外部结果、succeeded、failed、cancelled';

DROP INDEX agent_runs_active_scope_unique;
CREATE UNIQUE INDEX agent_runs_active_scope_unique
    ON agent_runs (organization_id, scope_kind, scope_id) WHERE (status = ANY (ARRAY['queued'::text, 'running'::text, 'waiting'::text]));

ALTER TABLE agent_run_blocks DROP COLUMN payload;
ALTER TABLE agent_run_blocks ADD COLUMN content text;
ALTER TABLE agent_run_blocks ADD COLUMN tool_call_id uuid;

COMMENT ON TABLE agent_run_blocks IS 'Agent 运行的有序中间内容，每次模型调用定稿后写入';
COMMENT ON COLUMN agent_run_blocks.content IS '思考或正文的完整文本，工具调用块为空';
COMMENT ON COLUMN agent_run_blocks.tool_call_id IS '工具调用块对应的工具调用编号，其他块为空';

-- +goose Down
ALTER TABLE agent_run_blocks DROP COLUMN tool_call_id;
ALTER TABLE agent_run_blocks DROP COLUMN content;
ALTER TABLE agent_run_blocks ADD COLUMN payload jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE agent_run_blocks ALTER COLUMN payload DROP DEFAULT;

COMMENT ON TABLE agent_run_blocks IS '已完成 Agent 运行的有序中间内容';
COMMENT ON COLUMN agent_run_blocks.payload IS '完整文本或工具调用参数、结果和状态';

DROP INDEX agent_runs_active_scope_unique;
CREATE UNIQUE INDEX agent_runs_active_scope_unique
    ON agent_runs (organization_id, scope_kind, scope_id) WHERE (status = ANY (ARRAY['queued'::text, 'running'::text]));

COMMENT ON COLUMN agent_runs.status IS '运行状态：queued、running、succeeded、failed、cancelled';
ALTER TABLE agent_runs DROP COLUMN task_run_id;
ALTER TABLE agent_runs DROP COLUMN state;

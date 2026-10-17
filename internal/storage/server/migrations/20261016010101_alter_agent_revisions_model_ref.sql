-- +goose Up
ALTER TABLE agent_revisions
    ALTER COLUMN model_id TYPE text USING model_id::text;

COMMENT ON COLUMN agent_revisions.model_id IS '托管执行使用的对话模型引用：auto 或工作区模型编号，本机 Agent 执行为空';

-- +goose Down
ALTER TABLE agent_revisions
    ALTER COLUMN model_id TYPE uuid USING NULLIF(model_id, 'auto')::uuid;

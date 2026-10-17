-- +goose Up
ALTER TABLE ai_model_calls
    ALTER COLUMN model_id TYPE text USING model_id::text;

COMMENT ON COLUMN ai_model_calls.model_id IS '调用的模型引用：auto 或工作区模型编号';

-- +goose Down
ALTER TABLE ai_model_calls
    ALTER COLUMN model_id TYPE uuid USING NULLIF(model_id, 'auto')::uuid;

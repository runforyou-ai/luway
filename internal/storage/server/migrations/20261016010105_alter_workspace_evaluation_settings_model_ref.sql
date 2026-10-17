-- +goose Up
ALTER TABLE workspace_evaluation_settings
    ALTER COLUMN decision_model_id TYPE text USING decision_model_id::text;

COMMENT ON COLUMN workspace_evaluation_settings.decision_model_id IS '评测使用的判断模型引用：auto 或工作区模型编号';

-- +goose Down
ALTER TABLE workspace_evaluation_settings
    ALTER COLUMN decision_model_id TYPE uuid USING NULLIF(decision_model_id, 'auto')::uuid;

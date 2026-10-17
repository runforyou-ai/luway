-- +goose Up
CREATE TABLE workspace_evaluation_settings (
    workspace_id uuid PRIMARY KEY,
    decision_model_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER workspace_evaluation_settings_set_updated_at BEFORE UPDATE ON workspace_evaluation_settings FOR EACH ROW EXECUTE FUNCTION set_updated_at();
COMMENT ON TABLE workspace_evaluation_settings IS '工作区 AI 评测设置';
COMMENT ON COLUMN workspace_evaluation_settings.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN workspace_evaluation_settings.decision_model_id IS '评测使用的判断模型编号';
COMMENT ON COLUMN workspace_evaluation_settings.created_at IS '创建时间';
COMMENT ON COLUMN workspace_evaluation_settings.updated_at IS '更新时间';
-- +goose Down
DROP TABLE workspace_evaluation_settings;

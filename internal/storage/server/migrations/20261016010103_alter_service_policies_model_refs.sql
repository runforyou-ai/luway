-- +goose Up
ALTER TABLE service_policies
    ALTER COLUMN decision_model_id TYPE text USING decision_model_id::text,
    ALTER COLUMN summary_model_id TYPE text USING summary_model_id::text;

COMMENT ON COLUMN service_policies.decision_model_id IS '标注周期实质诉求、咨询分类与是否解决的判断模型引用：auto 或工作区模型编号；为空时不标注';
COMMENT ON COLUMN service_policies.summary_model_id IS '生成周期小结与交接摘要的对话模型引用：auto 或工作区模型编号；为空时不生成正文';

-- +goose Down
ALTER TABLE service_policies
    ALTER COLUMN decision_model_id TYPE uuid USING NULLIF(decision_model_id, 'auto')::uuid,
    ALTER COLUMN summary_model_id TYPE uuid USING NULLIF(summary_model_id, 'auto')::uuid;

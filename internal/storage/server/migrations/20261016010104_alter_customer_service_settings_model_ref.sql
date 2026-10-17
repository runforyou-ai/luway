-- +goose Up
ALTER TABLE customer_service_settings
    ALTER COLUMN translation_model_id TYPE text USING translation_model_id::text;

COMMENT ON COLUMN customer_service_settings.translation_model_id IS '客户会话翻译模型引用：auto 或工作区模型编号';

-- +goose Down
ALTER TABLE customer_service_settings
    ALTER COLUMN translation_model_id TYPE uuid USING NULLIF(translation_model_id, 'auto')::uuid;

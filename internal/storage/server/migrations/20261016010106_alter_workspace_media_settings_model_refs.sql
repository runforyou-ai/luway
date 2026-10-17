-- +goose Up
ALTER TABLE workspace_media_settings
    ALTER COLUMN ocr_model_id TYPE text USING ocr_model_id::text,
    ALTER COLUMN transcription_model_id TYPE text USING transcription_model_id::text;

COMMENT ON COLUMN workspace_media_settings.ocr_model_id IS '图片文字识别兜底模型引用：auto 或工作区模型编号';
COMMENT ON COLUMN workspace_media_settings.transcription_model_id IS '语音转写兜底模型引用：auto 或工作区模型编号';

-- +goose Down
ALTER TABLE workspace_media_settings
    ALTER COLUMN ocr_model_id TYPE uuid USING NULLIF(ocr_model_id, 'auto')::uuid,
    ALTER COLUMN transcription_model_id TYPE uuid USING NULLIF(transcription_model_id, 'auto')::uuid;

-- +goose Up
-- 创建工作区媒体理解设置表，用于主模型不能看图或听语音时的文本投影。
CREATE TABLE workspace_media_settings (
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    workspace_id            uuid PRIMARY KEY,
    ocr_model_id            uuid,
    transcription_model_id  uuid
);

CREATE TRIGGER workspace_media_settings_set_updated_at BEFORE UPDATE ON workspace_media_settings FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE workspace_media_settings IS '工作区媒体理解设置；OCR 与语音转写模型可选';
COMMENT ON COLUMN workspace_media_settings.created_at IS '创建时间';
COMMENT ON COLUMN workspace_media_settings.updated_at IS '更新时间';
COMMENT ON COLUMN workspace_media_settings.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN workspace_media_settings.ocr_model_id IS '图片文字识别兜底模型编号';
COMMENT ON COLUMN workspace_media_settings.transcription_model_id IS '语音转写兜底模型编号';

-- +goose Down
DROP TABLE workspace_media_settings;

-- +goose Up
-- ai_models 仅保留工作区自有模型；平台能力改由 auto_pool_members 提供。
DELETE FROM ai_models WHERE workspace_id IS NULL;

DROP INDEX ai_models_platform_name_unique;

ALTER TABLE ai_models
    ALTER COLUMN workspace_id SET NOT NULL;

COMMENT ON TABLE ai_models IS '工作区自有 AI 模型，业务配置按模型编号或 auto 引用；平台 Auto 见 auto_pool_members';
COMMENT ON COLUMN ai_models.workspace_id IS '所属工作区编号';

-- +goose Down
ALTER TABLE ai_models
    ALTER COLUMN workspace_id DROP NOT NULL;

CREATE UNIQUE INDEX ai_models_platform_name_unique
    ON ai_models (lower(name)) WHERE (workspace_id IS NULL);

COMMENT ON TABLE ai_models IS 'AI 模型，业务配置按模型编号引用，调用来源见 ai_model_routes';
COMMENT ON COLUMN ai_models.workspace_id IS '所属工作区编号，为空表示平台提供的平台模型';

-- +goose Up
-- AI 模型只描述可选模型本身，作用域记在模型上，调用来源记在来源路由中。
ALTER TABLE ai_models
    DROP CONSTRAINT ai_models_provider_identifier_unique,
    DROP COLUMN provider_id,
    DROP COLUMN identifier,
    ADD COLUMN organization_id uuid;

COMMENT ON TABLE ai_models IS 'AI 模型，业务配置按模型编号引用，调用来源见 ai_model_routes';
COMMENT ON COLUMN ai_models.organization_id IS '所属工作区编号，为空表示部署提供的平台模型';

-- +goose Down
ALTER TABLE ai_models
    DROP COLUMN organization_id,
    ADD COLUMN provider_id uuid,
    ADD COLUMN identifier text;

UPDATE ai_models AS aim
SET provider_id = route.provider_id, identifier = route.identifier
FROM ai_model_routes AS route
WHERE route.model_id = aim.id;

DELETE FROM ai_models WHERE provider_id IS NULL;

ALTER TABLE ai_models
    ALTER COLUMN provider_id SET NOT NULL,
    ALTER COLUMN identifier SET NOT NULL,
    ADD CONSTRAINT ai_models_provider_identifier_unique UNIQUE (provider_id, identifier) DEFERRABLE INITIALLY DEFERRED;

COMMENT ON TABLE ai_models IS 'AI 模型，业务配置按模型编号引用；同一供应商内上游模型标识唯一，在事务提交时校验';
COMMENT ON COLUMN ai_models.provider_id IS '所属供应商编号，模型的作用域与所属工作区以供应商为准';
COMMENT ON COLUMN ai_models.identifier IS '调用上游时使用的模型标识';

-- +goose Up
-- 供应商所属工作区为空表示部署提供的平台供应商；平台模型名称在部署内唯一；调用记录标明模型范围。
ALTER TABLE ai_providers ALTER COLUMN organization_id DROP NOT NULL;

DROP INDEX ai_providers_organization_name_unique;
CREATE UNIQUE INDEX ai_providers_organization_name_unique
    ON ai_providers (organization_id, lower(name)) NULLS NOT DISTINCT;

CREATE UNIQUE INDEX ai_models_platform_name_unique
    ON ai_models (lower(name)) WHERE organization_id IS NULL;

ALTER TABLE ai_model_calls ADD COLUMN model_scope text NOT NULL DEFAULT 'workspace';
ALTER TABLE ai_model_calls ALTER COLUMN model_scope DROP DEFAULT;

COMMENT ON TABLE ai_providers IS 'AI 供应商，属于工作区或由部署提供';
COMMENT ON COLUMN ai_providers.organization_id IS '所属工作区编号，为空表示部署提供的平台供应商';
COMMENT ON COLUMN ai_model_calls.model_scope IS '模型范围：platform 平台模型、workspace 工作区模型';

-- +goose Down
ALTER TABLE ai_model_calls DROP COLUMN model_scope;

DROP INDEX ai_models_platform_name_unique;

DELETE FROM ai_model_routes WHERE model_id IN (SELECT id FROM ai_models WHERE organization_id IS NULL)
    OR provider_id IN (SELECT id FROM ai_providers WHERE organization_id IS NULL);
DELETE FROM ai_models WHERE organization_id IS NULL;
DELETE FROM ai_providers WHERE organization_id IS NULL;

DROP INDEX ai_providers_organization_name_unique;
CREATE UNIQUE INDEX ai_providers_organization_name_unique
    ON ai_providers (organization_id, lower(name));

ALTER TABLE ai_providers ALTER COLUMN organization_id SET NOT NULL;

COMMENT ON TABLE ai_providers IS '工作区 AI 供应商';
COMMENT ON COLUMN ai_providers.organization_id IS '所属工作区编号';

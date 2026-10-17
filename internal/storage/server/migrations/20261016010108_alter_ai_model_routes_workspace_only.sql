-- +goose Up
-- 平台模型改由 auto_pool_members 提供，清除原平台模型来源。
DELETE FROM ai_model_routes
WHERE model_id IN (SELECT id FROM ai_models WHERE workspace_id IS NULL);

COMMENT ON COLUMN ai_model_routes.provider_id IS '提供该来源的本工作区供应商编号';

-- +goose Down
COMMENT ON COLUMN ai_model_routes.provider_id IS '提供该来源的供应商编号；工作区模型指向本工作区供应商，平台模型指向平台供应商';

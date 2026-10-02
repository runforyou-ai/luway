-- +goose Up
-- 创建 AI 模型来源路由表。
CREATE TABLE ai_model_routes (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    model_id     uuid NOT NULL,
    provider_id  uuid NOT NULL,
    identifier   text NOT NULL,
    priority     integer NOT NULL DEFAULT 0,
    enabled      boolean NOT NULL DEFAULT true,
    CONSTRAINT ai_model_routes_provider_identifier_unique UNIQUE (provider_id, identifier) DEFERRABLE INITIALLY DEFERRED
);

COMMENT ON TABLE ai_model_routes IS 'AI 模型来源路由，调用模型时按优先级依次尝试已启用的来源；同一供应商内上游模型标识唯一，在事务提交时校验';
COMMENT ON COLUMN ai_model_routes.id IS '路由编号';
COMMENT ON COLUMN ai_model_routes.created_at IS '添加时间';
COMMENT ON COLUMN ai_model_routes.updated_at IS '更新时间';
COMMENT ON COLUMN ai_model_routes.model_id IS '所属模型编号';
COMMENT ON COLUMN ai_model_routes.provider_id IS '提供该来源的供应商编号；工作区模型指向本工作区供应商，平台模型指向平台供应商';
COMMENT ON COLUMN ai_model_routes.identifier IS '调用该来源时使用的上游模型标识';
COMMENT ON COLUMN ai_model_routes.priority IS '尝试顺序，数值小的先尝试';
COMMENT ON COLUMN ai_model_routes.enabled IS '是否参与调用';

-- +goose Down
DROP TABLE ai_model_routes;

-- +goose Up
-- 创建工作区 AI 供应商表。
CREATE TABLE ai_providers (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    organization_id  uuid NOT NULL,
    brand            text NOT NULL,
    name             text NOT NULL,
    credential_type  text NOT NULL,
    api_key          text NOT NULL,
    api_url          text NOT NULL
);

CREATE UNIQUE INDEX ai_providers_organization_name_unique
    ON ai_providers (organization_id, lower(name));

COMMENT ON TABLE ai_providers IS '工作区 AI 供应商';
COMMENT ON COLUMN ai_providers.id IS '供应商编号';
COMMENT ON COLUMN ai_providers.created_at IS '添加时间';
COMMENT ON COLUMN ai_providers.updated_at IS '更新时间';
COMMENT ON COLUMN ai_providers.organization_id IS '所属工作区编号';
COMMENT ON COLUMN ai_providers.brand IS '供应商品牌';
COMMENT ON COLUMN ai_providers.name IS '供应商名称';
COMMENT ON COLUMN ai_providers.credential_type IS '凭据类型：api_key 使用密钥，none 不需要凭据';
COMMENT ON COLUMN ai_providers.api_key IS 'API 密钥，凭据类型为 none 时为空';
COMMENT ON COLUMN ai_providers.api_url IS 'API 地址';

-- +goose Down
DROP TABLE ai_providers;

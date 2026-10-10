-- +goose Up
-- 创建 AI 供应商表。
CREATE TABLE ai_providers (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    workspace_id     uuid,
    brand            text NOT NULL,
    name             text NOT NULL,
    credential_type  text NOT NULL,
    api_key          text NOT NULL,
    api_url          text NOT NULL
);

CREATE UNIQUE INDEX ai_providers_workspace_name_unique
    ON ai_providers (workspace_id, lower(name)) NULLS NOT DISTINCT;

CREATE TRIGGER ai_providers_set_updated_at BEFORE UPDATE ON ai_providers FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE ai_providers IS 'AI 供应商，属于工作区或由平台提供';
COMMENT ON COLUMN ai_providers.id IS '供应商编号';
COMMENT ON COLUMN ai_providers.created_at IS '添加时间';
COMMENT ON COLUMN ai_providers.updated_at IS '更新时间';
COMMENT ON COLUMN ai_providers.workspace_id IS '所属工作区编号，为空表示平台提供的平台供应商';
COMMENT ON COLUMN ai_providers.brand IS '供应商品牌';
COMMENT ON COLUMN ai_providers.name IS '供应商名称';
COMMENT ON COLUMN ai_providers.credential_type IS '凭据类型：api_key 使用密钥，none 不需要凭据';
COMMENT ON COLUMN ai_providers.api_key IS 'API 密钥，凭据类型为 none 时为空';
COMMENT ON COLUMN ai_providers.api_url IS 'API 地址';

-- +goose Down
DROP TABLE ai_providers;

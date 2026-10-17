-- +goose Up
-- 创建工作区客户身份验证和翻译设置表。
CREATE TABLE customer_service_settings (
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    workspace_id               uuid PRIMARY KEY,
    customer_identity_secret   text,
    translation_model_id       uuid
);

CREATE TRIGGER customer_service_settings_set_updated_at BEFORE UPDATE ON customer_service_settings FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE customer_service_settings IS '工作区客户身份验证和翻译设置';
COMMENT ON COLUMN customer_service_settings.created_at IS '创建时间';
COMMENT ON COLUMN customer_service_settings.updated_at IS '更新时间';
COMMENT ON COLUMN customer_service_settings.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN customer_service_settings.customer_identity_secret IS '网站登录用户签名身份的 HMAC 密钥，base64url 字符串；未生成时为空，此时不接受签名身份';
COMMENT ON COLUMN customer_service_settings.translation_model_id IS '客户会话翻译模型编号';

-- +goose Down
DROP TABLE customer_service_settings;

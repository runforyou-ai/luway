-- +goose Up
-- 创建授权表。
CREATE TABLE licenses (
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    server_id           uuid PRIMARY KEY,
    license_id          uuid NOT NULL,
    customer            text NOT NULL,
    license_code        text NOT NULL,
    capabilities        jsonb NOT NULL,
    issued_at           timestamptz NOT NULL,
    expires_at          timestamptz NOT NULL,
    control_missing_at  timestamptz
);

CREATE TRIGGER licenses_set_updated_at BEFORE UPDATE ON licenses FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE licenses IS '平台当前生效的授权，每个服务器标识一行，签发时间更晚的授权码替换当前授权';
COMMENT ON COLUMN licenses.created_at IS '首次激活时间';
COMMENT ON COLUMN licenses.updated_at IS '更新时间';
COMMENT ON COLUMN licenses.server_id IS '授权绑定的服务器标识';
COMMENT ON COLUMN licenses.license_id IS '授权编号';
COMMENT ON COLUMN licenses.customer IS '客户名称';
COMMENT ON COLUMN licenses.license_code IS '已验签的授权码原文';
COMMENT ON COLUMN licenses.capabilities IS '授权码中的能力清单，只含与免费取值不同的能力键';
COMMENT ON COLUMN licenses.issued_at IS '授权码签发时间';
COMMENT ON COLUMN licenses.expires_at IS '授权期限';
COMMENT ON COLUMN licenses.control_missing_at IS '与 control 同步时查不到本服务器授权的起始时间，同步取得授权码或替换授权码时清空';

-- +goose Down
DROP TABLE licenses;

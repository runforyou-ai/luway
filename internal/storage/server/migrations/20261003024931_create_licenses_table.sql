-- +goose Up
-- 创建授权表。
CREATE TABLE licenses (
    server_id     uuid PRIMARY KEY,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    license_id    uuid NOT NULL,
    customer      text NOT NULL,
    license_code  text NOT NULL,
    capabilities  jsonb NOT NULL,
    issued_at     timestamptz NOT NULL,
    expires_at    timestamptz NOT NULL
);

COMMENT ON TABLE licenses IS '平台当前生效的授权，每个服务器标识一行，签发时间更晚的授权码替换当前授权';
COMMENT ON COLUMN licenses.server_id IS '授权绑定的服务器标识';
COMMENT ON COLUMN licenses.created_at IS '首次激活时间';
COMMENT ON COLUMN licenses.updated_at IS '最近替换授权码的时间';
COMMENT ON COLUMN licenses.license_id IS '授权编号';
COMMENT ON COLUMN licenses.customer IS '客户名称';
COMMENT ON COLUMN licenses.license_code IS '已验签的授权码原文';
COMMENT ON COLUMN licenses.capabilities IS '授权码中的能力清单，只含与免费取值不同的能力键';
COMMENT ON COLUMN licenses.issued_at IS '授权码签发时间';
COMMENT ON COLUMN licenses.expires_at IS '授权期限';

-- +goose Down
DROP TABLE licenses;

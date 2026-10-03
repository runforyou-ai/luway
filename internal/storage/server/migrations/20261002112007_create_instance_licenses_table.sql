-- +goose Up
-- 创建实例授权表。
CREATE TABLE instance_licenses (
    instance_id   uuid PRIMARY KEY,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    license_id    uuid NOT NULL,
    customer      text NOT NULL,
    license_code  text NOT NULL,
    capabilities  jsonb NOT NULL,
    issued_at     timestamptz NOT NULL,
    expires_at    timestamptz NOT NULL
);

COMMENT ON TABLE instance_licenses IS '实例当前生效的授权，每个实例一行，签发时间更晚的授权码替换当前授权';
COMMENT ON COLUMN instance_licenses.instance_id IS '授权绑定的实例标识';
COMMENT ON COLUMN instance_licenses.created_at IS '首次激活时间';
COMMENT ON COLUMN instance_licenses.updated_at IS '最近替换授权码的时间';
COMMENT ON COLUMN instance_licenses.license_id IS '授权编号';
COMMENT ON COLUMN instance_licenses.customer IS '客户名称';
COMMENT ON COLUMN instance_licenses.license_code IS '已验签的授权码原文';
COMMENT ON COLUMN instance_licenses.capabilities IS '授权码中的能力清单，只含与免费取值不同的能力键';
COMMENT ON COLUMN instance_licenses.issued_at IS '授权码签发时间';
COMMENT ON COLUMN instance_licenses.expires_at IS '授权期限';

-- +goose Down
DROP TABLE instance_licenses;

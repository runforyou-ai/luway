-- +goose Up
-- 创建工作区服务权益快照表。
CREATE TABLE organization_entitlements (
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    organization_id  uuid PRIMARY KEY,
    revision         bigint NOT NULL,
    plan_code        text NOT NULL,
    service_ends_at  timestamptz,
    applied_at       timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE organization_entitlements IS '工作区当前生效的服务权益快照';
COMMENT ON COLUMN organization_entitlements.created_at IS '创建时间';
COMMENT ON COLUMN organization_entitlements.updated_at IS '更新时间';
COMMENT ON COLUMN organization_entitlements.organization_id IS '工作区编号';
COMMENT ON COLUMN organization_entitlements.revision IS 'SaaS 递增的权益版本';
COMMENT ON COLUMN organization_entitlements.plan_code IS '套餐标识';
COMMENT ON COLUMN organization_entitlements.service_ends_at IS '服务截止时间，为空表示长期有效';
COMMENT ON COLUMN organization_entitlements.applied_at IS '最近一次应用时间';

-- +goose Down
DROP TABLE organization_entitlements;

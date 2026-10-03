-- +goose Up
-- 创建工作区权益表。
CREATE TABLE workspace_entitlements (
    organization_id  uuid PRIMARY KEY,
    updated_at       timestamptz NOT NULL DEFAULT now(),
    revision         bigint NOT NULL,
    plan_id          text NOT NULL,
    plan_name        text NOT NULL,
    seat_limit       integer NOT NULL,
    period_end       timestamptz
);

COMMENT ON TABLE workspace_entitlements IS '商业服务为工作区生成的当前生效权益，按版本号覆盖；解除配对时删除';
COMMENT ON COLUMN workspace_entitlements.organization_id IS '工作区编号';
COMMENT ON COLUMN workspace_entitlements.updated_at IS '最近应用权益的时间';
COMMENT ON COLUMN workspace_entitlements.revision IS '商业服务生成的权益版本号';
COMMENT ON COLUMN workspace_entitlements.plan_id IS '商业服务中的套餐编号';
COMMENT ON COLUMN workspace_entitlements.plan_name IS '套餐名称';
COMMENT ON COLUMN workspace_entitlements.seat_limit IS '席位上限，0 表示不限';
COMMENT ON COLUMN workspace_entitlements.period_end IS '当前套餐期限，为空表示长期有效';

-- +goose Down
DROP TABLE workspace_entitlements;

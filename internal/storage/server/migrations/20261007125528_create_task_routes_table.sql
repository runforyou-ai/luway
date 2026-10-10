-- +goose Up
-- 创建任务路由租约表。
CREATE TABLE task_routes (
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    route_key         text PRIMARY KEY,
    instance_id       uuid NOT NULL,
    lease_expires_at  timestamptz NOT NULL
);

CREATE TRIGGER task_routes_set_updated_at BEFORE UPDATE ON task_routes FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE task_routes IS '任务路由租约，带路由键的任务只由持有有效租约的服务端实例执行';
COMMENT ON COLUMN task_routes.created_at IS '创建时间';
COMMENT ON COLUMN task_routes.updated_at IS '更新时间';
COMMENT ON COLUMN task_routes.route_key IS '路由键';
COMMENT ON COLUMN task_routes.instance_id IS '持有租约的服务端实例编号';
COMMENT ON COLUMN task_routes.lease_expires_at IS '租约到期时间，到期后其他实例可接管';

-- +goose Down
DROP TABLE task_routes;

-- +goose Up
-- 创建角色权限关联表。
CREATE TABLE role_permissions (
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    role_id          uuid NOT NULL,
    permission       text NOT NULL,
    organization_id  uuid NOT NULL,
    PRIMARY KEY (role_id, permission)
);

COMMENT ON TABLE role_permissions IS '角色权限关联';
COMMENT ON COLUMN role_permissions.created_at IS '创建时间';
COMMENT ON COLUMN role_permissions.updated_at IS '更新时间';
COMMENT ON COLUMN role_permissions.role_id IS '角色编号';
COMMENT ON COLUMN role_permissions.permission IS '预定义权限代码';
COMMENT ON COLUMN role_permissions.organization_id IS '所属工作区编号';

-- +goose Down
DROP TABLE role_permissions;

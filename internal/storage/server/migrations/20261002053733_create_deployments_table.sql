-- +goose Up
-- 创建部署实例表。
CREATE TABLE deployments (
    instance_id                uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    registration_policy        text NOT NULL,
    workspace_creation_policy  text NOT NULL
);

CREATE UNIQUE INDEX deployments_singleton_unique
    ON deployments ((true));

COMMENT ON TABLE deployments IS '部署实例与部署级策略，首次安装时写入唯一一行';
COMMENT ON COLUMN deployments.instance_id IS '实例标识，首次安装时生成，之后不变';
COMMENT ON COLUMN deployments.created_at IS '创建时间，即首次安装时间';
COMMENT ON COLUMN deployments.updated_at IS '更新时间';
COMMENT ON COLUMN deployments.registration_policy IS '注册策略：open 开放注册，invitation_only 仅限受邀邮箱注册';
COMMENT ON COLUMN deployments.workspace_creation_policy IS '工作区创建策略：any_account 所有账号可创建，deployment_admin 仅部署管理员可创建';

-- +goose Down
DROP TABLE deployments;

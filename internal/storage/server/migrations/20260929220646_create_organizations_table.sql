-- +goose Up
-- 创建工作区表。
CREATE TABLE organizations (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    slug                 text NOT NULL,
    name                 text NOT NULL,
    lifecycle_status     text NOT NULL DEFAULT 'active',
    last_contact_number  bigint NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX organizations_slug_unique
    ON organizations (slug);

COMMENT ON TABLE organizations IS '工作区';
COMMENT ON COLUMN organizations.id IS '工作区编号';
COMMENT ON COLUMN organizations.created_at IS '创建时间';
COMMENT ON COLUMN organizations.updated_at IS '更新时间';
COMMENT ON COLUMN organizations.slug IS '工作区标识，全部署唯一，用于 Web 访问地址';
COMMENT ON COLUMN organizations.name IS '工作区名称';
COMMENT ON COLUMN organizations.lifecycle_status IS '工作区生命周期状态：active、suspended、deleting、deleted';
COMMENT ON COLUMN organizations.last_contact_number IS '工作区最近分配的联系人编号';

-- +goose Down
DROP TABLE organizations;

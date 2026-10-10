-- +goose Up
-- 创建工作区表。
CREATE TABLE workspaces (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    slug                 text NOT NULL,
    name                 text NOT NULL,
    lifecycle_status     text NOT NULL DEFAULT 'active',
    last_contact_number  bigint NOT NULL DEFAULT 0,
    created_country      text NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX workspaces_name_unique
    ON workspaces (lower(name));

CREATE UNIQUE INDEX workspaces_slug_unique
    ON workspaces (slug);

CREATE TRIGGER workspaces_set_updated_at BEFORE UPDATE ON workspaces FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE workspaces IS '工作区';
COMMENT ON COLUMN workspaces.id IS '工作区编号';
COMMENT ON COLUMN workspaces.created_at IS '创建时间';
COMMENT ON COLUMN workspaces.updated_at IS '更新时间';
COMMENT ON COLUMN workspaces.slug IS '工作区标识，全平台唯一，用于 Web 访问地址';
COMMENT ON COLUMN workspaces.name IS '工作区名称';
COMMENT ON COLUMN workspaces.lifecycle_status IS '工作区生命周期状态：active、suspended、deleting、deleted';
COMMENT ON COLUMN workspaces.last_contact_number IS '工作区最近分配的联系人编号';
COMMENT ON COLUMN workspaces.created_country IS '创建工作区的请求来源国家代码（ISO 3166-1 两位大写字母），由可信代理提供，未采集时为空';

-- +goose Down
DROP TABLE workspaces;

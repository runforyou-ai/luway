-- +goose Up
-- 创建联系人联系方式表。
CREATE TABLE contact_methods (
    id                uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    organization_id   uuid NOT NULL,
    contact_id        uuid NOT NULL,
    type              text NOT NULL,
    value             text NOT NULL,
    normalized_value  text NOT NULL,
    label             text,
    is_primary        boolean NOT NULL DEFAULT false
);

CREATE UNIQUE INDEX contact_methods_contact_primary_type_unique
    ON contact_methods (contact_id, type) WHERE is_primary;

CREATE UNIQUE INDEX contact_methods_contact_type_value_unique
    ON contact_methods (contact_id, type, normalized_value);

COMMENT ON TABLE contact_methods IS '联系人联系方式';
COMMENT ON COLUMN contact_methods.id IS '联系方式编号';
COMMENT ON COLUMN contact_methods.created_at IS '创建时间';
COMMENT ON COLUMN contact_methods.updated_at IS '更新时间';
COMMENT ON COLUMN contact_methods.organization_id IS '所属工作区编号';
COMMENT ON COLUMN contact_methods.contact_id IS '联系人编号';
COMMENT ON COLUMN contact_methods.type IS '联系方式类型';
COMMENT ON COLUMN contact_methods.value IS '联系方式原始值';
COMMENT ON COLUMN contact_methods.normalized_value IS '用于查询和去重的规范值';
COMMENT ON COLUMN contact_methods.label IS '自定义标签';
COMMENT ON COLUMN contact_methods.is_primary IS '是否为该类型的主要联系方式';

-- +goose Down
DROP TABLE contact_methods;

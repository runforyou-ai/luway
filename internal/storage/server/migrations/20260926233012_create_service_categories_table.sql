-- +goose Up
-- 创建工作区咨询分类表，团队关联由 Action 维护。
CREATE TABLE service_categories (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    organization_id  uuid NOT NULL,
    name             text NOT NULL,
    description      text NOT NULL DEFAULT '',
    team_id          uuid,
    archived_at      timestamptz
);

CREATE UNIQUE INDEX service_categories_organization_name_unique
    ON service_categories (organization_id, lower(name)) WHERE (archived_at IS NULL);

COMMENT ON TABLE service_categories IS '工作区咨询分类目录，用于转人工路由、周期小结与报表';
COMMENT ON COLUMN service_categories.id IS '咨询分类编号';
COMMENT ON COLUMN service_categories.created_at IS '创建时间';
COMMENT ON COLUMN service_categories.updated_at IS '更新时间';
COMMENT ON COLUMN service_categories.organization_id IS '所属工作区编号';
COMMENT ON COLUMN service_categories.name IS '分类名称，未归档的分类在工作区内唯一';
COMMENT ON COLUMN service_categories.description IS '供 AI 判断归类的说明';
COMMENT ON COLUMN service_categories.team_id IS 'AI 转人工时承接该分类的团队；为空时按渠道失败路由';
COMMENT ON COLUMN service_categories.archived_at IS '归档时间；已归档的分类只用于展示历史记录';

-- +goose Down
DROP TABLE service_categories;

-- +goose Up
ALTER TABLE workspaces DROP COLUMN created_country;

-- +goose Down
ALTER TABLE workspaces ADD COLUMN created_country text NOT NULL DEFAULT '';

COMMENT ON COLUMN workspaces.created_country IS '创建工作区的请求来源国家代码（ISO 3166-1 两位大写字母），由可信代理提供，未采集时为空';

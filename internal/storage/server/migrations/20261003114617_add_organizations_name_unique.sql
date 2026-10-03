-- +goose Up
-- 工作区名称在部署内按小写值保持唯一。
CREATE UNIQUE INDEX organizations_name_unique ON organizations (lower(name));

-- +goose Down
DROP INDEX organizations_name_unique;

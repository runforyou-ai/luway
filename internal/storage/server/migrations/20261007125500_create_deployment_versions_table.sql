-- +goose Up
-- 创建部署版本表。
CREATE TABLE deployment_versions (
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    version     text PRIMARY KEY
);

CREATE UNIQUE INDEX deployment_versions_singleton_unique
    ON deployment_versions ((true));

CREATE TRIGGER deployment_versions_set_updated_at BEFORE UPDATE ON deployment_versions FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE deployment_versions IS '部署当前运行的服务端版本，唯一一行；版本更高或显式回退的服务端启动时改写，版本与之不同的服务端进程拒绝启动或自动退出';
COMMENT ON COLUMN deployment_versions.created_at IS '创建时间';
COMMENT ON COLUMN deployment_versions.updated_at IS '更新时间';
COMMENT ON COLUMN deployment_versions.version IS '服务端版本';

-- +goose Down
DROP TABLE deployment_versions;

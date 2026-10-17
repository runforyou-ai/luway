-- +goose Up
-- 创建工作区网页读取设置表；没有记录时使用内置直连抓取。
CREATE TABLE web_fetch_settings (
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    workspace_id  uuid PRIMARY KEY,
    provider      text NOT NULL,
    api_key       text NOT NULL DEFAULT '',
    base_url      text NOT NULL DEFAULT ''
);

CREATE TRIGGER web_fetch_settings_set_updated_at BEFORE UPDATE ON web_fetch_settings FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE web_fetch_settings IS '工作区网页读取设置；没有记录表示使用内置直连抓取';
COMMENT ON COLUMN web_fetch_settings.created_at IS '创建时间';
COMMENT ON COLUMN web_fetch_settings.updated_at IS '更新时间';
COMMENT ON COLUMN web_fetch_settings.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN web_fetch_settings.provider IS '网页读取服务商';
COMMENT ON COLUMN web_fetch_settings.api_key IS '读取服务 API Key，内置直连为空';
COMMENT ON COLUMN web_fetch_settings.base_url IS '自建读取服务的实例地址，官方服务为空';

-- +goose Down
DROP TABLE web_fetch_settings;

-- +goose Up
-- 创建工作区联网搜索设置表，每个工作区至多启用一个搜索服务。
CREATE TABLE web_search_settings (
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    organization_id  uuid PRIMARY KEY,
    provider         text NOT NULL,
    api_key          text NOT NULL DEFAULT '',
    base_url         text NOT NULL DEFAULT ''
);

COMMENT ON TABLE web_search_settings IS '工作区联网搜索设置；没有记录表示未启用联网搜索';
COMMENT ON COLUMN web_search_settings.created_at IS '创建时间';
COMMENT ON COLUMN web_search_settings.updated_at IS '更新时间';
COMMENT ON COLUMN web_search_settings.organization_id IS '所属工作区编号';
COMMENT ON COLUMN web_search_settings.provider IS '搜索服务商';
COMMENT ON COLUMN web_search_settings.api_key IS '搜索服务 API Key，自托管服务为空';
COMMENT ON COLUMN web_search_settings.base_url IS '自托管搜索服务的实例地址，其他服务商为空';

-- +goose Down
DROP TABLE web_search_settings;

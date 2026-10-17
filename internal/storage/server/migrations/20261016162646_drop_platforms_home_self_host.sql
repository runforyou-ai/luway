-- +goose Up
ALTER TABLE platforms
    DROP COLUMN home_self_host;

-- +goose Down
ALTER TABLE platforms
    ADD COLUMN home_self_host boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN platforms.home_self_host IS '产品首页提供价格区块时是否展示自部署介绍；不提供价格区块时首页始终展示';

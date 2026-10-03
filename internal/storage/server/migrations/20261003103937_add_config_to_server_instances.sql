-- +goose Up
-- 服务器实例记录进程启动时的配置。
ALTER TABLE server_instances ADD COLUMN config jsonb NOT NULL DEFAULT '{}';

COMMENT ON COLUMN server_instances.config IS '进程启动时的服务端配置，只含不带密码、密钥和地址凭据的字段';

-- +goose Down
ALTER TABLE server_instances DROP COLUMN config;

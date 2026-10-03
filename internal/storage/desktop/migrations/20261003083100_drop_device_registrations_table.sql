-- +goose Up
-- 本机改为以电脑凭据连接服务端，删除设备注册结果表。
DROP TABLE device_registrations;

-- +goose Down
CREATE TABLE device_registrations (
    server_url      text NOT NULL,
    account_id      text NOT NULL,
    organization_id text NOT NULL,
    device_id       text NOT NULL,
    registered_at   text NOT NULL,
    PRIMARY KEY (server_url, account_id, organization_id)
);

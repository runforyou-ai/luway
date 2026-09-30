-- +goose Up
-- 创建桌面端本机设备按「服务器地址 + 账号 + 工作区」登记的注册结果表。
CREATE TABLE device_registrations (
    server_url      text NOT NULL,
    account_id      text NOT NULL,
    organization_id text NOT NULL,
    device_id       text NOT NULL,
    registered_at   text NOT NULL,
    PRIMARY KEY (server_url, account_id, organization_id)
);

-- +goose Down
DROP TABLE device_registrations;

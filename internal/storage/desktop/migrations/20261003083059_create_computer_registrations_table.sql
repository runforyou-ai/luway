-- +goose Up
-- 创建本机电脑按「服务器地址 + 账号 + 工作区」登记的注册结果与电脑凭据表。
CREATE TABLE computer_registrations (
    server_url      text NOT NULL,
    account_id      text NOT NULL,
    organization_id text NOT NULL,
    computer_id     text NOT NULL,
    credential      text NOT NULL,
    registered_at   text NOT NULL,
    PRIMARY KEY (server_url, account_id, organization_id)
);

-- +goose Down
DROP TABLE computer_registrations;

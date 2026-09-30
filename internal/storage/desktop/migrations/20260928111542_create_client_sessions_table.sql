-- +goose Up
-- 创建桌面端当前登录会话表。
CREATE TABLE client_sessions (
    id          text PRIMARY KEY,
    server_url  text NOT NULL,
    account_id  text NOT NULL,
    token       text NOT NULL,
    expires_at  text NOT NULL
);

-- +goose Down
DROP TABLE client_sessions;

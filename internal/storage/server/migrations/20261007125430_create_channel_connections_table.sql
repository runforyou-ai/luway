-- +goose Up
-- 创建渠道长连接状态表。
CREATE TABLE channel_connections (
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    channel_id    uuid PRIMARY KEY,
    workspace_id  uuid NOT NULL,
    status        text NOT NULL,
    last_error    text NOT NULL DEFAULT '',
    connected_at  timestamptz
);

CREATE TRIGGER channel_connections_set_updated_at BEFORE UPDATE ON channel_connections FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE channel_connections IS '渠道长连接状态，由持有渠道任务路由租约的服务端实例建立连接并写入';
COMMENT ON COLUMN channel_connections.created_at IS '创建时间';
COMMENT ON COLUMN channel_connections.updated_at IS '更新时间';
COMMENT ON COLUMN channel_connections.channel_id IS '渠道编号';
COMMENT ON COLUMN channel_connections.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN channel_connections.status IS '连接状态：connecting 连接中，online 在线，replaced 被其他连接替换，rejected 平台拒绝凭据，offline 连接中断';
COMMENT ON COLUMN channel_connections.last_error IS '最近一次连接失败的原因码';
COMMENT ON COLUMN channel_connections.connected_at IS '当前连接建立时间，未在线时为空';

-- +goose Down
DROP TABLE channel_connections;

-- +goose Up
-- 创建渠道发送等待状态表。
CREATE TABLE channel_send_gates (
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    channel_id        uuid PRIMARY KEY,
    workspace_id      uuid NOT NULL,
    flood_wait_until  timestamptz NOT NULL
);

CREATE TRIGGER channel_send_gates_set_updated_at BEFORE UPDATE ON channel_send_gates FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE channel_send_gates IS '渠道发送等待状态';
COMMENT ON COLUMN channel_send_gates.created_at IS '创建时间';
COMMENT ON COLUMN channel_send_gates.updated_at IS '更新时间';
COMMENT ON COLUMN channel_send_gates.channel_id IS '渠道编号';
COMMENT ON COLUMN channel_send_gates.workspace_id IS '工作区编号';
COMMENT ON COLUMN channel_send_gates.flood_wait_until IS '平台限流等待截止时间';

-- +goose Down
DROP TABLE channel_send_gates;

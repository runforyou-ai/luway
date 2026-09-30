-- +goose Up
-- 创建客户渠道发送等待状态表。
CREATE TABLE customer_channel_send_gates (
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    channel_id        uuid PRIMARY KEY,
    organization_id   uuid NOT NULL,
    flood_wait_until  timestamptz NOT NULL
);

COMMENT ON TABLE customer_channel_send_gates IS '客户渠道发送等待状态';
COMMENT ON COLUMN customer_channel_send_gates.created_at IS '创建时间';
COMMENT ON COLUMN customer_channel_send_gates.updated_at IS '更新时间';
COMMENT ON COLUMN customer_channel_send_gates.channel_id IS '渠道编号';
COMMENT ON COLUMN customer_channel_send_gates.organization_id IS '工作区编号';
COMMENT ON COLUMN customer_channel_send_gates.flood_wait_until IS '平台限流等待截止时间';

-- +goose Down
DROP TABLE customer_channel_send_gates;

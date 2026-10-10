-- +goose Up
-- 创建渠道消息投递请求项表。
CREATE TABLE channel_delivery_items (
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    delivery_id          uuid NOT NULL,
    seq                  integer NOT NULL,
    workspace_id         uuid NOT NULL,
    body                 text NOT NULL DEFAULT '',
    attachment           boolean NOT NULL DEFAULT false,
    provider_message_id  text,
    sent_at              timestamptz,
    PRIMARY KEY (delivery_id, seq)
);

CREATE TRIGGER channel_delivery_items_set_updated_at BEFORE UPDATE ON channel_delivery_items FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE channel_delivery_items IS '渠道消息投递按平台请求拆分的有序请求项';
COMMENT ON COLUMN channel_delivery_items.created_at IS '创建时间';
COMMENT ON COLUMN channel_delivery_items.updated_at IS '更新时间';
COMMENT ON COLUMN channel_delivery_items.delivery_id IS '所属投递编号';
COMMENT ON COLUMN channel_delivery_items.seq IS '投递内的发送顺序，从 1 开始';
COMMENT ON COLUMN channel_delivery_items.workspace_id IS '工作区编号';
COMMENT ON COLUMN channel_delivery_items.body IS '本请求发送的文本或附件说明';
COMMENT ON COLUMN channel_delivery_items.attachment IS '本请求是否发送所属消息的附件';
COMMENT ON COLUMN channel_delivery_items.provider_message_id IS '平台返回的消息编号；平台未返回时为空';
COMMENT ON COLUMN channel_delivery_items.sent_at IS '发送确认时间，未确认发送时为空';

-- +goose Down
DROP TABLE channel_delivery_items;

-- +goose Up
CREATE TABLE message_outbox (
 id uuid PRIMARY KEY,
 kind text NOT NULL,
 payload jsonb NOT NULL,
 available_at timestamptz NOT NULL DEFAULT now(),
 attempts integer NOT NULL DEFAULT 0,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX message_outbox_pending_idx ON message_outbox (kind, available_at, created_at, id);
CREATE TRIGGER message_outbox_set_updated_at BEFORE UPDATE ON message_outbox FOR EACH ROW EXECUTE FUNCTION set_updated_at();
COMMENT ON TABLE message_outbox IS '事务内登记的待投递消息批次';
COMMENT ON COLUMN message_outbox.id IS '投递批次编号';
COMMENT ON COLUMN message_outbox.kind IS '消息类型';
COMMENT ON COLUMN message_outbox.payload IS '按登记顺序编码的消息';
COMMENT ON COLUMN message_outbox.available_at IS '下次可投递时间';
COMMENT ON COLUMN message_outbox.attempts IS '失败尝试次数';
COMMENT ON COLUMN message_outbox.created_at IS '创建时间';
COMMENT ON COLUMN message_outbox.updated_at IS '更新时间';
-- +goose Down
DROP TABLE message_outbox;

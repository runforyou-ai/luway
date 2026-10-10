-- +goose Up
-- 创建渠道回复窗口表。
CREATE TABLE channel_reply_windows (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    workspace_id         uuid NOT NULL,
    channel_identity_id  uuid NOT NULL,
    trigger              text NOT NULL,
    opened_at            timestamptz NOT NULL,
    expires_at           timestamptz NOT NULL,
    quota                integer,
    used                 integer NOT NULL DEFAULT 0,
    reserved             integer NOT NULL DEFAULT 0
);

CREATE INDEX channel_reply_windows_identity_idx
    ON channel_reply_windows (workspace_id, channel_identity_id, trigger, opened_at);

CREATE TRIGGER channel_reply_windows_set_updated_at BEFORE UPDATE ON channel_reply_windows FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE channel_reply_windows IS '外部平台允许向渠道身份发送消息的回复窗口';
COMMENT ON COLUMN channel_reply_windows.id IS '回复窗口编号';
COMMENT ON COLUMN channel_reply_windows.created_at IS '创建时间';
COMMENT ON COLUMN channel_reply_windows.updated_at IS '更新时间';
COMMENT ON COLUMN channel_reply_windows.workspace_id IS '工作区编号';
COMMENT ON COLUMN channel_reply_windows.channel_identity_id IS '渠道身份编号';
COMMENT ON COLUMN channel_reply_windows.trigger IS '开启窗口的平台触发动作，由渠道适配器定义；同一渠道身份同一触发动作只有开启时间最新的窗口可用于发送';
COMMENT ON COLUMN channel_reply_windows.opened_at IS '窗口开启时间';
COMMENT ON COLUMN channel_reply_windows.expires_at IS '窗口到期时间';
COMMENT ON COLUMN channel_reply_windows.quota IS '窗口内允许发送的平台请求数；不限条数时为空';
COMMENT ON COLUMN channel_reply_windows.used IS '已确认发送的平台请求数';
COMMENT ON COLUMN channel_reply_windows.reserved IS '已预占且结果未定的平台请求数';

-- +goose Down
DROP TABLE channel_reply_windows;

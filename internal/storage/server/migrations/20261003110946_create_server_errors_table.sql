-- +goose Up
-- 创建服务端错误记录表。
CREATE TABLE server_errors (
    id           uuid PRIMARY KEY,
    occurred_at  timestamptz NOT NULL,
    instance_id  uuid NOT NULL,
    hostname     text NOT NULL,
    version      text NOT NULL,
    message      text NOT NULL,
    operation    text,
    action       text,
    queue        text,
    error        text,
    event_id     text,
    attributes   jsonb NOT NULL
);

COMMENT ON TABLE server_errors IS '服务端 Error 级别日志，各进程异步批量写入，保留 7 天';
COMMENT ON COLUMN server_errors.id IS '错误记录编号';
COMMENT ON COLUMN server_errors.occurred_at IS '日志记录时间';
COMMENT ON COLUMN server_errors.instance_id IS '写入日志的服务端进程实例编号';
COMMENT ON COLUMN server_errors.hostname IS '服务端进程所在主机名';
COMMENT ON COLUMN server_errors.version IS '服务端版本';
COMMENT ON COLUMN server_errors.message IS '日志消息';
COMMENT ON COLUMN server_errors.operation IS '出错的业务入口方法';
COMMENT ON COLUMN server_errors.action IS '出错的后台任务名称';
COMMENT ON COLUMN server_errors.queue IS '出错的后台任务所在队列';
COMMENT ON COLUMN server_errors.error IS '错误信息原文';
COMMENT ON COLUMN server_errors.event_id IS '上报 control 的错误事件编号，未上报时为空';
COMMENT ON COLUMN server_errors.attributes IS '日志的其他属性，键为属性名，值为文本';

-- +goose Down
DROP TABLE server_errors;

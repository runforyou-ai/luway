-- +goose Up
-- 创建服务端日志表，按记录时间以 UTC 自然日分区，分区由服务端维护任务提前创建并在保留期后整体删除。
CREATE TABLE server_logs (
    id            uuid NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    occurred_at   timestamptz NOT NULL,
    level         smallint NOT NULL,
    instance_id   uuid NOT NULL,
    hostname      text NOT NULL,
    version       text NOT NULL,
    message       text NOT NULL,
    trace_id      text,
    operation     text,
    task_run_id   text,
    action        text,
    queue         text,
    workspace_id  text,
    account_id    text,
    error         text,
    event_id      text,
    attributes    jsonb NOT NULL,
    PRIMARY KEY (occurred_at, id)
) PARTITION BY RANGE (occurred_at);

CREATE INDEX server_logs_action_idx
    ON server_logs (action, occurred_at) WHERE (action IS NOT NULL);

CREATE INDEX server_logs_instance_idx
    ON server_logs (instance_id, occurred_at);

CREATE INDEX server_logs_operation_idx
    ON server_logs (operation, occurred_at) WHERE (operation IS NOT NULL);

CREATE INDEX server_logs_trace_idx
    ON server_logs (trace_id, occurred_at) WHERE (trace_id IS NOT NULL);

CREATE INDEX server_logs_warning_idx
    ON server_logs (occurred_at, id) WHERE (level >= 4);

CREATE INDEX server_logs_workspace_idx
    ON server_logs (workspace_id, occurred_at) WHERE (workspace_id IS NOT NULL);

CREATE TRIGGER server_logs_set_updated_at BEFORE UPDATE ON server_logs FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE server_logs IS '服务端日志，各进程异步批量写入全部级别，按 UTC 自然日分区，保留 30 天';
COMMENT ON COLUMN server_logs.id IS '日志记录编号';
COMMENT ON COLUMN server_logs.created_at IS '创建时间';
COMMENT ON COLUMN server_logs.updated_at IS '更新时间';
COMMENT ON COLUMN server_logs.occurred_at IS '日志记录时间';
COMMENT ON COLUMN server_logs.level IS '日志级别，取 slog 级别数值：-4 调试、0 信息、4 警告、8 错误';
COMMENT ON COLUMN server_logs.instance_id IS '写入日志的服务端进程实例编号';
COMMENT ON COLUMN server_logs.hostname IS '服务端进程所在主机名';
COMMENT ON COLUMN server_logs.version IS '服务端版本';
COMMENT ON COLUMN server_logs.message IS '日志消息';
COMMENT ON COLUMN server_logs.trace_id IS '串联编号，同一次请求或定时触发及其后续异步任务共用';
COMMENT ON COLUMN server_logs.operation IS '业务入口方法名，不经业务入口的 HTTP 请求为路由';
COMMENT ON COLUMN server_logs.task_run_id IS '正在执行的异步任务运行编号';
COMMENT ON COLUMN server_logs.action IS '正在执行的异步任务名称';
COMMENT ON COLUMN server_logs.queue IS '正在执行的异步任务所在队列';
COMMENT ON COLUMN server_logs.workspace_id IS '日志所属工作区编号，取自日志作用域';
COMMENT ON COLUMN server_logs.account_id IS '发起请求的登录账号编号，取自日志作用域';
COMMENT ON COLUMN server_logs.error IS '错误信息原文';
COMMENT ON COLUMN server_logs.event_id IS '上报 control 的错误事件编号，未上报时为空';
COMMENT ON COLUMN server_logs.attributes IS '日志的其他属性，键为分组路径加属性名，值为文本';

-- +goose Down
DROP TABLE server_logs;

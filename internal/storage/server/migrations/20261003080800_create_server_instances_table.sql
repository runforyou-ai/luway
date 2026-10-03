-- +goose Up
-- 创建服务器实例表。
CREATE TABLE server_instances (
    id              uuid PRIMARY KEY,
    started_at      timestamptz NOT NULL DEFAULT now(),
    heartbeat_at    timestamptz NOT NULL DEFAULT now(),
    hostname        text NOT NULL,
    version                  text NOT NULL,
    tasks_nats_connected     boolean NOT NULL,
    realtime_nats_connected  boolean NOT NULL
);

COMMENT ON TABLE server_instances IS '运行中的服务端进程，启动时登记、定时刷新心跳，正常退出时删除，心跳超过 1 小时的记录由其他进程删除';
COMMENT ON COLUMN server_instances.id IS '进程实例编号，与后台任务 Worker 标识的前缀一致';
COMMENT ON COLUMN server_instances.started_at IS '进程启动时间';
COMMENT ON COLUMN server_instances.heartbeat_at IS '最近一次心跳时间';
COMMENT ON COLUMN server_instances.hostname IS '进程所在主机名';
COMMENT ON COLUMN server_instances.version IS '服务端版本';
COMMENT ON COLUMN server_instances.tasks_nats_connected IS '最近一次心跳时后台任务的 NATS 连接是否可用';
COMMENT ON COLUMN server_instances.realtime_nats_connected IS '最近一次心跳时实时通知的 NATS 连接是否可用';

-- +goose Down
DROP TABLE server_instances;

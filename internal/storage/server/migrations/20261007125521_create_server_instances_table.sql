-- +goose Up
-- 创建服务器实例表。
CREATE TABLE server_instances (
    id                uuid PRIMARY KEY,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    started_at        timestamptz NOT NULL DEFAULT now(),
    heartbeat_at      timestamptz NOT NULL DEFAULT now(),
    hostname          text NOT NULL,
    version           text NOT NULL,
    bus_connected     boolean NOT NULL,
    config            jsonb NOT NULL DEFAULT '{}'::jsonb,
    bus_driver        text NOT NULL,
    bus_message_rate  double precision NOT NULL DEFAULT 0,
    bus_failures      integer NOT NULL DEFAULT 0
);

CREATE TRIGGER server_instances_set_updated_at BEFORE UPDATE ON server_instances FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE server_instances IS '运行中的服务端进程，启动时登记、定时刷新心跳，正常退出时删除，心跳超过 1 小时的记录由其他进程删除';
COMMENT ON COLUMN server_instances.id IS '进程实例编号，与后台任务 Worker 标识的前缀一致';
COMMENT ON COLUMN server_instances.created_at IS '创建时间';
COMMENT ON COLUMN server_instances.updated_at IS '更新时间';
COMMENT ON COLUMN server_instances.started_at IS '进程启动时间';
COMMENT ON COLUMN server_instances.heartbeat_at IS '最近一次心跳时间';
COMMENT ON COLUMN server_instances.hostname IS '进程所在主机名';
COMMENT ON COLUMN server_instances.version IS '服务端版本';
COMMENT ON COLUMN server_instances.bus_connected IS '最近一次心跳时消息总线是否可用';
COMMENT ON COLUMN server_instances.config IS '进程启动时的服务端配置，只含不带密码、密钥和地址凭据的字段';
COMMENT ON COLUMN server_instances.bus_driver IS '消息总线的传输驱动：postgres 或 nats';
COMMENT ON COLUMN server_instances.bus_message_rate IS '上一个心跳间隔内经消息总线发送的消息数（条/秒）';
COMMENT ON COLUMN server_instances.bus_failures IS '上一个心跳间隔内消息总线发送失败与丢弃的消息数';

-- +goose Down
DROP TABLE server_instances;

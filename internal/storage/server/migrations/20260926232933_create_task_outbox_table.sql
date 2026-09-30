-- +goose Up
-- 创建任务消息发件箱表。
CREATE TABLE task_outbox (
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    task_run_id   uuid PRIMARY KEY,
    message_id    uuid NOT NULL DEFAULT uuidv7(),
    queue_name    text NOT NULL,
    attempts      integer NOT NULL DEFAULT 0,
    available_at  timestamptz NOT NULL DEFAULT now(),
    last_error    text
);

COMMENT ON TABLE task_outbox IS '等待可靠发布到 NATS 的任务消息';
COMMENT ON COLUMN task_outbox.created_at IS '创建时间';
COMMENT ON COLUMN task_outbox.updated_at IS '更新时间';
COMMENT ON COLUMN task_outbox.task_run_id IS '关联任务运行编号';
COMMENT ON COLUMN task_outbox.message_id IS '当前逻辑投递的消息幂等编号';
COMMENT ON COLUMN task_outbox.queue_name IS '目标任务队列';
COMMENT ON COLUMN task_outbox.attempts IS '已经尝试发布的次数';
COMMENT ON COLUMN task_outbox.available_at IS '允许下次发布的时间';
COMMENT ON COLUMN task_outbox.last_error IS '最近一次发布错误';

-- +goose Down
DROP TABLE task_outbox;

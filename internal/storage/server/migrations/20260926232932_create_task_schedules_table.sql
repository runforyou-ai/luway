-- +goose Up
-- 创建定时任务计划表。
CREATE TABLE task_schedules (
    id                uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    schedule_key      text NOT NULL,
    action_name       text NOT NULL,
    queue_name        text NOT NULL,
    payload           jsonb NOT NULL DEFAULT '{}'::jsonb,
    cron_expression   text NOT NULL,
    timezone          text NOT NULL,
    enabled           boolean NOT NULL DEFAULT true,
    max_attempts      integer NOT NULL,
    next_run_at       timestamptz NOT NULL,
    last_enqueued_at  timestamptz
);

CREATE UNIQUE INDEX task_schedules_key_unique
    ON task_schedules (schedule_key);

COMMENT ON TABLE task_schedules IS '服务端定时 Action 计划';
COMMENT ON COLUMN task_schedules.id IS '计划编号';
COMMENT ON COLUMN task_schedules.created_at IS '创建时间';
COMMENT ON COLUMN task_schedules.updated_at IS '更新时间';
COMMENT ON COLUMN task_schedules.schedule_key IS '代码和管理端使用的稳定计划标识';
COMMENT ON COLUMN task_schedules.action_name IS '计划触发的 Action 名称';
COMMENT ON COLUMN task_schedules.queue_name IS '计划使用的任务队列';
COMMENT ON COLUMN task_schedules.payload IS 'Action 输入参数';
COMMENT ON COLUMN task_schedules.cron_expression IS 'Cron 或固定周期表达式';
COMMENT ON COLUMN task_schedules.timezone IS '计划解释时区';
COMMENT ON COLUMN task_schedules.enabled IS '是否启用计划';
COMMENT ON COLUMN task_schedules.max_attempts IS '每次任务最多执行次数';
COMMENT ON COLUMN task_schedules.next_run_at IS '下一次待触发时间';
COMMENT ON COLUMN task_schedules.last_enqueued_at IS '最近一次已创建任务的计划时间';

-- +goose Down
DROP TABLE task_schedules;

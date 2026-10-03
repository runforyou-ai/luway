-- +goose Up
-- 创建异步任务运行记录表。
CREATE TABLE task_runs (
    id                uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    action_name       text NOT NULL,
    queue_name        text NOT NULL,
    payload           jsonb NOT NULL DEFAULT '{}'::jsonb,
    trigger_type      text NOT NULL,
    schedule_key      text,
    status            text NOT NULL,
    attempt           integer NOT NULL DEFAULT 0,
    max_attempts      integer NOT NULL,
    available_at      timestamptz NOT NULL DEFAULT now(),
    lease_expires_at  timestamptz,
    worker_id         text,
    idempotency_key   text,
    published_at      timestamptz,
    started_at        timestamptz,
    completed_at      timestamptz,
    last_error        text,
    organization_id   uuid,
    failed_at         timestamptz
);

CREATE UNIQUE INDEX task_runs_active_idempotency_unique
    ON task_runs (action_name, idempotency_key) WHERE ((idempotency_key IS NOT NULL) AND (schedule_key IS NULL) AND (status = ANY (ARRAY['queued'::text, 'published'::text, 'running'::text, 'retrying'::text, 'paused'::text])));

CREATE UNIQUE INDEX task_runs_schedule_occurrence_unique
    ON task_runs (schedule_key, idempotency_key) WHERE ((schedule_key IS NOT NULL) AND (idempotency_key IS NOT NULL));

COMMENT ON TABLE task_runs IS '服务端异步任务运行记录';
COMMENT ON COLUMN task_runs.id IS '任务运行编号';
COMMENT ON COLUMN task_runs.created_at IS '创建时间';
COMMENT ON COLUMN task_runs.updated_at IS '更新时间';
COMMENT ON COLUMN task_runs.action_name IS '注册的 Action 名称';
COMMENT ON COLUMN task_runs.queue_name IS '投递队列名称';
COMMENT ON COLUMN task_runs.payload IS 'Action 输入参数';
COMMENT ON COLUMN task_runs.trigger_type IS '手动、业务或定时触发类型';
COMMENT ON COLUMN task_runs.schedule_key IS '来源定时计划标识';
COMMENT ON COLUMN task_runs.status IS '任务运行状态：queued、published、running、retrying、paused 所属工作区暂停中、succeeded、failed';
COMMENT ON COLUMN task_runs.attempt IS '已经开始的执行次数';
COMMENT ON COLUMN task_runs.max_attempts IS '最多执行次数';
COMMENT ON COLUMN task_runs.available_at IS '允许下次执行的时间';
COMMENT ON COLUMN task_runs.lease_expires_at IS '当前 Worker 租约过期时间';
COMMENT ON COLUMN task_runs.worker_id IS '当前执行 Worker 标识';
COMMENT ON COLUMN task_runs.idempotency_key IS '活动任务幂等标识';
COMMENT ON COLUMN task_runs.published_at IS '最近一次发布到消息队列的时间';
COMMENT ON COLUMN task_runs.started_at IS '首次开始执行时间';
COMMENT ON COLUMN task_runs.completed_at IS '最终完成时间';
COMMENT ON COLUMN task_runs.last_error IS '最近一次执行错误';
COMMENT ON COLUMN task_runs.organization_id IS '任务所属工作区编号，平台级任务为空';
COMMENT ON COLUMN task_runs.failed_at IS '最近一次执行失败的时间，与 last_error 同时写入；执行成功时清空';

-- +goose Down
DROP TABLE task_runs;

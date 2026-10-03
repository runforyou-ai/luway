-- +goose Up
-- 任务运行记录最近一次执行失败的时间。
ALTER TABLE task_runs
    ADD COLUMN failed_at timestamptz;

COMMENT ON COLUMN task_runs.failed_at IS '最近一次执行失败的时间，与 last_error 同时写入；执行成功时清空';

-- +goose Down
ALTER TABLE task_runs
    DROP COLUMN failed_at;

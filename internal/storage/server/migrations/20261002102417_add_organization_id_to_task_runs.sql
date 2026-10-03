-- +goose Up
-- 任务运行记录所属工作区，工作区暂停期间任务进入 paused 状态。
ALTER TABLE task_runs
    ADD COLUMN organization_id uuid;

DROP INDEX task_runs_active_idempotency_unique;

CREATE UNIQUE INDEX task_runs_active_idempotency_unique
    ON task_runs (action_name, idempotency_key) WHERE ((idempotency_key IS NOT NULL) AND (schedule_key IS NULL) AND (status = ANY (ARRAY['queued'::text, 'published'::text, 'running'::text, 'retrying'::text, 'paused'::text])));

COMMENT ON COLUMN task_runs.organization_id IS '任务所属工作区编号，部署级任务为空';
COMMENT ON COLUMN task_runs.status IS '任务运行状态：queued、published、running、retrying、paused 所属工作区暂停中、succeeded、failed';

-- +goose Down
COMMENT ON COLUMN task_runs.status IS '任务运行状态';

DROP INDEX task_runs_active_idempotency_unique;

CREATE UNIQUE INDEX task_runs_active_idempotency_unique
    ON task_runs (action_name, idempotency_key) WHERE ((idempotency_key IS NOT NULL) AND (schedule_key IS NULL) AND (status = ANY (ARRAY['queued'::text, 'published'::text, 'running'::text, 'retrying'::text])));

ALTER TABLE task_runs
    DROP COLUMN organization_id;

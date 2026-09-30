-- +goose Up
-- 创建 AI 员工评测运行表，同一 AI 员工同一时间只有一个进行中的运行。
CREATE TABLE agent_evaluation_runs (
    id                      uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    organization_id         uuid NOT NULL,
    agent_id                uuid NOT NULL,
    agent_revision_id       uuid NOT NULL,
    status                  text NOT NULL DEFAULT 'running',
    case_count              integer NOT NULL,
    passed_count            integer NOT NULL DEFAULT 0,
    failed_count            integer NOT NULL DEFAULT 0,
    error_count             integer NOT NULL DEFAULT 0,
    started_by_identity_id  uuid NOT NULL,
    completed_at            timestamptz
);

CREATE UNIQUE INDEX agent_evaluation_runs_running_agent_unique
    ON agent_evaluation_runs (organization_id, agent_id) WHERE (status = 'running'::text);

COMMENT ON TABLE agent_evaluation_runs IS 'AI 员工评测运行：用一个配置版本对 AI 员工的全部用例各跑一次';
COMMENT ON COLUMN agent_evaluation_runs.id IS '评测运行编号';
COMMENT ON COLUMN agent_evaluation_runs.created_at IS '创建时间，即发起时间';
COMMENT ON COLUMN agent_evaluation_runs.updated_at IS '更新时间';
COMMENT ON COLUMN agent_evaluation_runs.organization_id IS '所属工作区编号';
COMMENT ON COLUMN agent_evaluation_runs.agent_id IS '被评测的 AI 员工编号';
COMMENT ON COLUMN agent_evaluation_runs.agent_revision_id IS '发起时生效的 AI 员工配置版本编号';
COMMENT ON COLUMN agent_evaluation_runs.status IS '状态：running 进行中、completed 已完成';
COMMENT ON COLUMN agent_evaluation_runs.case_count IS '发起时的用例数';
COMMENT ON COLUMN agent_evaluation_runs.passed_count IS '首次尝试通过的用例数';
COMMENT ON COLUMN agent_evaluation_runs.failed_count IS '首次尝试未通过的用例数';
COMMENT ON COLUMN agent_evaluation_runs.error_count IS '首次尝试评测异常的用例数';
COMMENT ON COLUMN agent_evaluation_runs.started_by_identity_id IS '发起运行的成员工作区身份编号';
COMMENT ON COLUMN agent_evaluation_runs.completed_at IS '全部首次尝试结束的时间';
COMMENT ON INDEX agent_evaluation_runs_running_agent_unique IS '同一 AI 员工进行中评测运行唯一索引';

-- +goose Down
DROP TABLE agent_evaluation_runs;

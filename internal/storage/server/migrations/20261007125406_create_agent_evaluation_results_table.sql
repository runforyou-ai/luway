-- +goose Up
-- 创建 AI 员工评测结果表，每次运行中每条用例的每次尝试一行。
CREATE TABLE agent_evaluation_results (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    workspace_id         uuid NOT NULL,
    run_id               uuid NOT NULL,
    case_id              uuid NOT NULL,
    attempt              integer NOT NULL,
    case_version         integer NOT NULL,
    case_snapshot        jsonb NOT NULL,
    status               text NOT NULL DEFAULT 'pending',
    actual_action        text,
    actual_reason        text,
    answer               text NOT NULL DEFAULT '',
    blocks               jsonb NOT NULL DEFAULT '[]'::jsonb,
    correct_probability  double precision,
    usage                jsonb NOT NULL DEFAULT '{}'::jsonb,
    error                text,
    completed_at         timestamptz
);

CREATE UNIQUE INDEX agent_evaluation_results_run_case_attempt_unique
    ON agent_evaluation_results (run_id, case_id, attempt);

CREATE TRIGGER agent_evaluation_results_set_updated_at BEFORE UPDATE ON agent_evaluation_results FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE agent_evaluation_results IS 'AI 员工评测结果：一次运行中一条用例的一次尝试';
COMMENT ON COLUMN agent_evaluation_results.id IS '评测结果编号';
COMMENT ON COLUMN agent_evaluation_results.created_at IS '创建时间';
COMMENT ON COLUMN agent_evaluation_results.updated_at IS '更新时间';
COMMENT ON COLUMN agent_evaluation_results.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN agent_evaluation_results.run_id IS '所属评测运行编号';
COMMENT ON COLUMN agent_evaluation_results.case_id IS '评测用例编号，用例删除后保留';
COMMENT ON COLUMN agent_evaluation_results.attempt IS '尝试序号：随运行发起的首次尝试为 1，单条重跑依次递增';
COMMENT ON COLUMN agent_evaluation_results.case_version IS '运行发起时的用例版本号';
COMMENT ON COLUMN agent_evaluation_results.case_snapshot IS '运行发起时冻结的用例快照：服务对象、提问、期望处理方式与标准答案';
COMMENT ON COLUMN agent_evaluation_results.status IS '状态：pending 进行中、passed 通过、failed 未通过、error 评测异常';
COMMENT ON COLUMN agent_evaluation_results.actual_action IS 'AI 实际的处理方式：reply、ask_customer、resolve、handoff，异常时为空';
COMMENT ON COLUMN agent_evaluation_results.actual_reason IS 'AI 转人工的原因，其他处理方式为空';
COMMENT ON COLUMN agent_evaluation_results.answer IS 'AI 发给提问人的正文';
COMMENT ON COLUMN agent_evaluation_results.blocks IS '运行过程内容块';
COMMENT ON COLUMN agent_evaluation_results.correct_probability IS '判断模型给出的答案符合标准答案的概率，未调用判断模型时为空';
COMMENT ON COLUMN agent_evaluation_results.usage IS '模型用量';
COMMENT ON COLUMN agent_evaluation_results.error IS '评测异常的原因';
COMMENT ON COLUMN agent_evaluation_results.completed_at IS '结束时间';
COMMENT ON INDEX agent_evaluation_results_run_case_attempt_unique IS '运行内用例尝试唯一索引';

-- +goose Down
DROP TABLE agent_evaluation_results;

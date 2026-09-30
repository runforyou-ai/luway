-- +goose Up
-- 创建 AI 员工评测用例表，用例保存提问、期望处理方式与标准答案。
CREATE TABLE agent_evaluation_cases (
    id                      uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    organization_id         uuid NOT NULL,
    agent_id                uuid NOT NULL,
    version                 integer NOT NULL DEFAULT 1,
    audience                text NOT NULL,
    question                text NOT NULL,
    expected_action         text NOT NULL,
    expected_answer         text NOT NULL DEFAULT '',
    created_by_identity_id  uuid NOT NULL,
    source                  text NOT NULL DEFAULT 'manual',
    service_session_id      uuid,
    question_message_id     uuid,
    occurred_at             timestamptz,
    context                 jsonb
);

CREATE UNIQUE INDEX agent_evaluation_cases_question_message_unique
    ON agent_evaluation_cases (organization_id, question_message_id) WHERE (question_message_id IS NOT NULL);

COMMENT ON TABLE agent_evaluation_cases IS 'AI 员工评测用例：一个提问及其期望处理方式和标准答案';
COMMENT ON COLUMN agent_evaluation_cases.id IS '评测用例编号';
COMMENT ON COLUMN agent_evaluation_cases.created_at IS '创建时间';
COMMENT ON COLUMN agent_evaluation_cases.updated_at IS '更新时间';
COMMENT ON COLUMN agent_evaluation_cases.organization_id IS '所属工作区编号';
COMMENT ON COLUMN agent_evaluation_cases.agent_id IS '所属 AI 员工编号';
COMMENT ON COLUMN agent_evaluation_cases.version IS '用例版本号，每次修改加一';
COMMENT ON COLUMN agent_evaluation_cases.audience IS '提问人所属服务对象：customer 客户、employee 员工';
COMMENT ON COLUMN agent_evaluation_cases.question IS '提问';
COMMENT ON COLUMN agent_evaluation_cases.expected_action IS '期望处理方式：reply 答复、ask_customer 追问、resolve 答复并结束会话、handoff 转人工';
COMMENT ON COLUMN agent_evaluation_cases.expected_answer IS '标准答案的要点或处理规则，期望转人工时为空字符串';
COMMENT ON COLUMN agent_evaluation_cases.created_by_identity_id IS '创建用例的成员工作区身份编号';
COMMENT ON COLUMN agent_evaluation_cases.source IS '来源：manual 手动、knowledge_gap 待补知识、service_session 问题会话';
COMMENT ON COLUMN agent_evaluation_cases.service_session_id IS '来源服务周期编号，手动用例为空';
COMMENT ON COLUMN agent_evaluation_cases.question_message_id IS '来源周期中作为提问的客户消息编号，手动用例为空';
COMMENT ON COLUMN agent_evaluation_cases.occurred_at IS '所选提问消息的时间，手动用例为空';
COMMENT ON COLUMN agent_evaluation_cases.context IS '加入时的快照：前文、客户上下文正文、客户已验证身份与来源是否为渠道会话，手动用例为空';
COMMENT ON INDEX agent_evaluation_cases_question_message_unique IS '同一提问消息只加入一条评测用例';

-- +goose Down
DROP TABLE agent_evaluation_cases;

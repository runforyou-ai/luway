-- +goose Up
-- 创建 AI 模型调用记录表。
CREATE TABLE ai_model_calls (
    id                    uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    finished_at           timestamptz,
    workspace_id          uuid NOT NULL,
    model_id              uuid NOT NULL,
    model_name            text NOT NULL,
    model_usage           text NOT NULL,
    actor_type            text NOT NULL,
    actor_id              uuid,
    source_type           text NOT NULL,
    source_id             uuid,
    status                text NOT NULL,
    input_tokens          bigint NOT NULL DEFAULT 0,
    cached_input_tokens   bigint NOT NULL DEFAULT 0,
    output_tokens         bigint NOT NULL DEFAULT 0,
    error_message         text NOT NULL DEFAULT '',
    model_scope           text NOT NULL
);

CREATE INDEX ai_model_calls_created_idx
    ON ai_model_calls (created_at DESC, id DESC);

CREATE INDEX ai_model_calls_running_idx
    ON ai_model_calls (created_at) WHERE (status = 'running'::text);

CREATE TRIGGER ai_model_calls_set_updated_at BEFORE UPDATE ON ai_model_calls FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE ai_model_calls IS 'AI 模型调用记录，每次业务模型请求一条，不保存提示词与回复正文';
COMMENT ON COLUMN ai_model_calls.id IS '调用编号';
COMMENT ON COLUMN ai_model_calls.created_at IS '开始时间';
COMMENT ON COLUMN ai_model_calls.updated_at IS '更新时间';
COMMENT ON COLUMN ai_model_calls.finished_at IS '结束时间，进行中为空';
COMMENT ON COLUMN ai_model_calls.workspace_id IS '调用归属的工作区编号';
COMMENT ON COLUMN ai_model_calls.model_id IS '调用的模型编号';
COMMENT ON COLUMN ai_model_calls.model_name IS '调用时的模型名称';
COMMENT ON COLUMN ai_model_calls.model_usage IS '模型用途：agent AI 员工、summary 摘要、translation 翻译、decision 判断、embedding 向量化、rerank 重排';
COMMENT ON COLUMN ai_model_calls.actor_type IS '发起主体类型：member 成员、agent AI 员工、system 后台任务';
COMMENT ON COLUMN ai_model_calls.actor_id IS '发起主体编号：成员与 AI 员工为工作区身份编号，后台任务为空';
COMMENT ON COLUMN ai_model_calls.source_type IS '调用所服务的业务对象类型：agent_run AI 员工运行、agent_evaluation 评测、conversation 会话、service_session 客服周期、knowledge_base 知识库、knowledge_document 知识文档、knowledge_qa_entry 知识问答、channel 渠道';
COMMENT ON COLUMN ai_model_calls.source_id IS '调用所服务的业务对象编号';
COMMENT ON COLUMN ai_model_calls.status IS '调用状态：running 进行中、succeeded 成功、failed 失败、canceled 已取消、timed_out 超时';
COMMENT ON COLUMN ai_model_calls.input_tokens IS '输入 Token 数，含命中缓存的部分';
COMMENT ON COLUMN ai_model_calls.cached_input_tokens IS '命中缓存的输入 Token 数';
COMMENT ON COLUMN ai_model_calls.output_tokens IS '输出 Token 数';
COMMENT ON COLUMN ai_model_calls.error_message IS '失败、取消或超时的原因';
COMMENT ON COLUMN ai_model_calls.model_scope IS '模型范围：platform 平台模型、workspace 工作区模型';

-- +goose Down
DROP TABLE ai_model_calls;

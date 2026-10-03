-- +goose Up
-- 创建 AI 模型上游尝试表。
CREATE TABLE ai_model_call_attempts (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at           timestamptz NOT NULL DEFAULT now(),
    finished_at          timestamptz,
    call_id              uuid NOT NULL,
    route_id             uuid NOT NULL,
    provider_id          uuid NOT NULL,
    provider_name        text NOT NULL,
    identifier           text NOT NULL,
    status               text NOT NULL,
    input_tokens         bigint NOT NULL DEFAULT 0,
    cached_input_tokens  bigint NOT NULL DEFAULT 0,
    output_tokens        bigint NOT NULL DEFAULT 0,
    error_message        text NOT NULL DEFAULT ''
);

COMMENT ON TABLE ai_model_call_attempts IS 'AI 模型上游尝试，一次调用按来源路由依次请求上游，每次请求一条';
COMMENT ON COLUMN ai_model_call_attempts.id IS '尝试编号，按编号排序即尝试顺序';
COMMENT ON COLUMN ai_model_call_attempts.created_at IS '开始时间';
COMMENT ON COLUMN ai_model_call_attempts.finished_at IS '结束时间，进行中为空';
COMMENT ON COLUMN ai_model_call_attempts.call_id IS '所属调用编号';
COMMENT ON COLUMN ai_model_call_attempts.route_id IS '使用的来源路由编号';
COMMENT ON COLUMN ai_model_call_attempts.provider_id IS '请求的供应商编号';
COMMENT ON COLUMN ai_model_call_attempts.provider_name IS '请求时的供应商名称';
COMMENT ON COLUMN ai_model_call_attempts.identifier IS '请求时使用的上游模型标识';
COMMENT ON COLUMN ai_model_call_attempts.status IS '尝试状态：running 进行中、succeeded 成功、failed 失败、canceled 已取消、timed_out 超时';
COMMENT ON COLUMN ai_model_call_attempts.input_tokens IS '输入 Token 数，含命中缓存的部分';
COMMENT ON COLUMN ai_model_call_attempts.cached_input_tokens IS '命中缓存的输入 Token 数';
COMMENT ON COLUMN ai_model_call_attempts.output_tokens IS '输出 Token 数';
COMMENT ON COLUMN ai_model_call_attempts.error_message IS '失败、取消或超时的原因';

-- +goose Down
DROP TABLE ai_model_call_attempts;

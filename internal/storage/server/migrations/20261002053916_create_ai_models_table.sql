-- +goose Up
-- 创建 AI 模型表。
CREATE TABLE ai_models (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    provider_id        uuid NOT NULL,
    identifier         text NOT NULL,
    name               text NOT NULL,
    model_type         text NOT NULL,
    input_modalities   jsonb NOT NULL,
    context_window     bigint NOT NULL,
    max_output_tokens  bigint NOT NULL,
    CONSTRAINT ai_models_provider_identifier_unique UNIQUE (provider_id, identifier) DEFERRABLE INITIALLY DEFERRED
);

COMMENT ON TABLE ai_models IS 'AI 模型，业务配置按模型编号引用；同一供应商内上游模型标识唯一，在事务提交时校验';
COMMENT ON COLUMN ai_models.id IS '模型编号';
COMMENT ON COLUMN ai_models.created_at IS '添加时间';
COMMENT ON COLUMN ai_models.updated_at IS '更新时间';
COMMENT ON COLUMN ai_models.provider_id IS '所属供应商编号，模型的作用域与所属工作区以供应商为准';
COMMENT ON COLUMN ai_models.identifier IS '调用上游时使用的模型标识';
COMMENT ON COLUMN ai_models.name IS '模型名称';
COMMENT ON COLUMN ai_models.model_type IS '模型类型：chat 对话、embedding 向量、rerank 重排、decision 判断';
COMMENT ON COLUMN ai_models.input_modalities IS '支持的输入模态';
COMMENT ON COLUMN ai_models.context_window IS '上下文窗口 Token 数';
COMMENT ON COLUMN ai_models.max_output_tokens IS '对话模型最大输出 Token 数，其他类型为 0';

-- +goose Down
DROP TABLE ai_models;

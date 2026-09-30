-- +goose Up
-- 创建模型服务供应商模型表。
CREATE TABLE ai_provider_models (
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    provider_id        uuid NOT NULL,
    identifier         text NOT NULL,
    organization_id    uuid NOT NULL,
    name               text NOT NULL,
    model_type         text NOT NULL,
    input_modalities   jsonb NOT NULL,
    context_window     bigint NOT NULL,
    max_output_tokens  bigint NOT NULL,
    PRIMARY KEY (provider_id, identifier)
);

COMMENT ON TABLE ai_provider_models IS '模型服务供应商模型目录';
COMMENT ON COLUMN ai_provider_models.created_at IS '添加时间';
COMMENT ON COLUMN ai_provider_models.updated_at IS '更新时间';
COMMENT ON COLUMN ai_provider_models.provider_id IS '供应商编号';
COMMENT ON COLUMN ai_provider_models.identifier IS '模型标识';
COMMENT ON COLUMN ai_provider_models.organization_id IS '所属工作区编号';
COMMENT ON COLUMN ai_provider_models.name IS '模型名称';
COMMENT ON COLUMN ai_provider_models.model_type IS '模型用途';
COMMENT ON COLUMN ai_provider_models.input_modalities IS '支持的输入模态';
COMMENT ON COLUMN ai_provider_models.context_window IS '上下文窗口 Token 数';
COMMENT ON COLUMN ai_provider_models.max_output_tokens IS '对话模型最大输出 Token 数';

-- +goose Down
DROP TABLE ai_provider_models;

-- +goose Up
-- 创建 AI 模型表。
CREATE TABLE ai_models (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    name               text NOT NULL,
    model_type         text NOT NULL,
    input_modalities   jsonb NOT NULL,
    context_window     bigint NOT NULL,
    max_output_tokens  bigint NOT NULL,
    organization_id    uuid
);

CREATE UNIQUE INDEX ai_models_platform_name_unique
    ON ai_models (lower(name)) WHERE (organization_id IS NULL);

COMMENT ON TABLE ai_models IS 'AI 模型，业务配置按模型编号引用，调用来源见 ai_model_routes';
COMMENT ON COLUMN ai_models.id IS '模型编号';
COMMENT ON COLUMN ai_models.created_at IS '添加时间';
COMMENT ON COLUMN ai_models.updated_at IS '更新时间';
COMMENT ON COLUMN ai_models.name IS '模型名称';
COMMENT ON COLUMN ai_models.model_type IS '模型类型：chat 对话、embedding 向量、rerank 重排、decision 判断';
COMMENT ON COLUMN ai_models.input_modalities IS '支持的输入模态';
COMMENT ON COLUMN ai_models.context_window IS '上下文窗口 Token 数';
COMMENT ON COLUMN ai_models.max_output_tokens IS '对话模型最大输出 Token 数，其他类型为 0';
COMMENT ON COLUMN ai_models.organization_id IS '所属工作区编号，为空表示平台提供的平台模型';

-- +goose Down
DROP TABLE ai_models;

-- +goose Up
-- 业务配置按模型编号引用 AI 模型，删除供应商模型目录表与任务向量模型快照。
DROP TABLE ai_provider_models;

ALTER TABLE agent_revisions ADD COLUMN model_id uuid;
COMMENT ON COLUMN agent_revisions.model_id IS '托管执行使用的对话模型编号，本机 Agent 执行为空';
COMMENT ON COLUMN agent_revisions.configuration IS '非敏感执行配置快照，不含模型引用';

ALTER TABLE knowledge_bases
    DROP COLUMN embedding_provider_id,
    DROP COLUMN embedding_model_identifier,
    DROP COLUMN rerank_provider_id,
    DROP COLUMN rerank_model_identifier,
    ADD COLUMN embedding_model_id uuid NOT NULL,
    ADD COLUMN rerank_model_id uuid NOT NULL;
COMMENT ON COLUMN knowledge_bases.embedding_model_id IS '向量模型编号';
COMMENT ON COLUMN knowledge_bases.rerank_model_id IS '重排模型编号';

ALTER TABLE knowledge_documents
    DROP COLUMN embedding_provider_id,
    DROP COLUMN embedding_model_identifier,
    DROP COLUMN embedding_dimension;

ALTER TABLE knowledge_qa_entries
    DROP COLUMN embedding_provider_id,
    DROP COLUMN embedding_model_identifier,
    DROP COLUMN embedding_dimension;

ALTER TABLE customer_service_settings
    DROP COLUMN decision_provider_id,
    DROP COLUMN decision_model_identifier,
    DROP COLUMN summary_provider_id,
    DROP COLUMN summary_model_identifier,
    DROP COLUMN translation_provider_id,
    DROP COLUMN translation_model_identifier,
    ADD COLUMN decision_model_id uuid,
    ADD COLUMN summary_model_id uuid,
    ADD COLUMN translation_model_id uuid;
COMMENT ON COLUMN customer_service_settings.decision_model_id IS '标注周期实质诉求、咨询分类与是否解决的判断模型编号；为空时不标注';
COMMENT ON COLUMN customer_service_settings.summary_model_id IS '生成周期小结与交接摘要的对话模型编号；为空时不生成正文';
COMMENT ON COLUMN customer_service_settings.translation_model_id IS '翻译客户会话消息的对话模型编号；为空时不提供翻译';

-- +goose Down
ALTER TABLE customer_service_settings
    DROP COLUMN decision_model_id,
    DROP COLUMN summary_model_id,
    DROP COLUMN translation_model_id,
    ADD COLUMN decision_provider_id uuid,
    ADD COLUMN decision_model_identifier text,
    ADD COLUMN summary_provider_id uuid,
    ADD COLUMN summary_model_identifier text,
    ADD COLUMN translation_provider_id uuid,
    ADD COLUMN translation_model_identifier text;
COMMENT ON COLUMN customer_service_settings.decision_provider_id IS '标注周期实质诉求、咨询分类与是否解决的判断模型供应商编号；为空时不标注';
COMMENT ON COLUMN customer_service_settings.decision_model_identifier IS '判断模型标识';
COMMENT ON COLUMN customer_service_settings.summary_provider_id IS '生成周期小结与交接摘要的对话模型供应商编号；为空时不生成正文';
COMMENT ON COLUMN customer_service_settings.summary_model_identifier IS '小结模型标识';
COMMENT ON COLUMN customer_service_settings.translation_provider_id IS '翻译客户会话消息的对话模型供应商编号；为空时不提供翻译';
COMMENT ON COLUMN customer_service_settings.translation_model_identifier IS '翻译模型标识';

ALTER TABLE knowledge_qa_entries
    ADD COLUMN embedding_provider_id uuid,
    ADD COLUMN embedding_model_identifier text NOT NULL DEFAULT '',
    ADD COLUMN embedding_dimension integer NOT NULL DEFAULT 0;
COMMENT ON COLUMN knowledge_qa_entries.embedding_provider_id IS '任务向量模型供应商编号';
COMMENT ON COLUMN knowledge_qa_entries.embedding_model_identifier IS '任务向量模型标识';
COMMENT ON COLUMN knowledge_qa_entries.embedding_dimension IS '任务向量维度';

ALTER TABLE knowledge_documents
    ADD COLUMN embedding_provider_id uuid,
    ADD COLUMN embedding_model_identifier text NOT NULL DEFAULT '',
    ADD COLUMN embedding_dimension integer NOT NULL DEFAULT 0;
COMMENT ON COLUMN knowledge_documents.embedding_provider_id IS '任务向量模型供应商编号';
COMMENT ON COLUMN knowledge_documents.embedding_model_identifier IS '任务向量模型标识';
COMMENT ON COLUMN knowledge_documents.embedding_dimension IS '任务向量维度';

ALTER TABLE knowledge_bases
    DROP COLUMN embedding_model_id,
    DROP COLUMN rerank_model_id,
    ADD COLUMN embedding_provider_id uuid,
    ADD COLUMN embedding_model_identifier text,
    ADD COLUMN rerank_provider_id uuid NOT NULL,
    ADD COLUMN rerank_model_identifier text NOT NULL;
COMMENT ON COLUMN knowledge_bases.embedding_provider_id IS '向量模型供应商编号';
COMMENT ON COLUMN knowledge_bases.embedding_model_identifier IS '向量模型标识';
COMMENT ON COLUMN knowledge_bases.rerank_provider_id IS '重排模型供应商编号';
COMMENT ON COLUMN knowledge_bases.rerank_model_identifier IS '重排模型标识';

COMMENT ON COLUMN agent_revisions.configuration IS '非敏感执行配置快照';
ALTER TABLE agent_revisions DROP COLUMN model_id;

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

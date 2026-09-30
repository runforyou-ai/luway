-- +goose Up
-- 创建工作区知识库表。
CREATE TABLE knowledge_bases (
    id                          uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    organization_id             uuid NOT NULL,
    created_by_user_id          uuid NOT NULL,
    name                        text NOT NULL,
    category                    text NOT NULL,
    description                 text NOT NULL DEFAULT '',
    embedding_provider_id       uuid,
    embedding_model_identifier  text,
    embedding_dimension         bigint,
    chunk_length                integer,
    chunk_overlap               integer,
    retrieval_count             integer,
    retrieval_score_threshold   double precision NOT NULL,
    rerank_provider_id          uuid NOT NULL,
    rerank_model_identifier     text NOT NULL
);

CREATE UNIQUE INDEX knowledge_bases_organization_name_unique
    ON knowledge_bases (organization_id, lower(name));

COMMENT ON TABLE knowledge_bases IS '工作区知识库';
COMMENT ON COLUMN knowledge_bases.id IS '知识库编号';
COMMENT ON COLUMN knowledge_bases.created_at IS '创建时间';
COMMENT ON COLUMN knowledge_bases.updated_at IS '更新时间';
COMMENT ON COLUMN knowledge_bases.organization_id IS '所属工作区编号';
COMMENT ON COLUMN knowledge_bases.created_by_user_id IS '创建用户编号';
COMMENT ON COLUMN knowledge_bases.name IS '知识库名称';
COMMENT ON COLUMN knowledge_bases.category IS '内容类型：standard 文档库、qa 问答库';
COMMENT ON COLUMN knowledge_bases.description IS '知识库描述';
COMMENT ON COLUMN knowledge_bases.embedding_provider_id IS '向量模型供应商编号';
COMMENT ON COLUMN knowledge_bases.embedding_model_identifier IS '向量模型标识';
COMMENT ON COLUMN knowledge_bases.embedding_dimension IS '向量维度';
COMMENT ON COLUMN knowledge_bases.chunk_length IS '分段长度，问答库不设置';
COMMENT ON COLUMN knowledge_bases.chunk_overlap IS '分段重叠，问答库不设置';
COMMENT ON COLUMN knowledge_bases.retrieval_count IS '召回数量';
COMMENT ON COLUMN knowledge_bases.retrieval_score_threshold IS '相关性阈值，重排得分低于该值的内容不返回';
COMMENT ON COLUMN knowledge_bases.rerank_provider_id IS '重排模型供应商编号';
COMMENT ON COLUMN knowledge_bases.rerank_model_identifier IS '重排模型标识';

-- +goose Down
DROP TABLE knowledge_bases;

-- +goose Up
ALTER TABLE knowledge_bases
    ALTER COLUMN embedding_model_id TYPE text USING embedding_model_id::text,
    ALTER COLUMN rerank_model_id TYPE text USING rerank_model_id::text,
    ADD COLUMN embedding_sticky_member_id uuid;

COMMENT ON COLUMN knowledge_bases.embedding_model_id IS '向量模型引用：auto 或工作区模型编号';
COMMENT ON COLUMN knowledge_bases.rerank_model_id IS '重排模型引用：auto 或工作区模型编号';
COMMENT ON COLUMN knowledge_bases.embedding_sticky_member_id IS 'embedding 选用 Auto 时钉住的池成员编号；首次成功选型后写入，之后优先使用';

-- +goose Down
ALTER TABLE knowledge_bases
    DROP COLUMN embedding_sticky_member_id,
    ALTER COLUMN embedding_model_id TYPE uuid USING NULLIF(embedding_model_id, 'auto')::uuid,
    ALTER COLUMN rerank_model_id TYPE uuid USING NULLIF(rerank_model_id, 'auto')::uuid;

-- +goose Up
-- 创建知识库文件文档表。
CREATE TABLE knowledge_documents (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    knowledge_base_id   uuid NOT NULL,
    source_kind         character varying(16) NOT NULL DEFAULT 'file'::character varying,
    file_id             uuid,
    title               text NOT NULL DEFAULT '',
    source_url          text NOT NULL DEFAULT '',
    status              character varying(32) NOT NULL DEFAULT 'initial'::character varying,
    processing_id       uuid,
    segment_batch_id    uuid,
    segment_count       integer NOT NULL DEFAULT 0,
    failure_code        character varying(64) NOT NULL DEFAULT ''::character varying,
    chunk_length        integer NOT NULL DEFAULT 512,
    chunk_overlap       integer NOT NULL DEFAULT 50,
    created_by_user_id  uuid NOT NULL
);

CREATE UNIQUE INDEX knowledge_documents_file_unique
    ON knowledge_documents (file_id) WHERE (file_id IS NOT NULL);

CREATE UNIQUE INDEX knowledge_documents_source_url_unique
    ON knowledge_documents (knowledge_base_id, source_url) WHERE ((source_kind)::text = 'web'::text);

COMMENT ON TABLE knowledge_documents IS '知识库文件文档';
COMMENT ON COLUMN knowledge_documents.id IS '文档编号';
COMMENT ON COLUMN knowledge_documents.created_at IS '创建时间';
COMMENT ON COLUMN knowledge_documents.updated_at IS '更新时间';
COMMENT ON COLUMN knowledge_documents.knowledge_base_id IS '所属知识库编号';
COMMENT ON COLUMN knowledge_documents.source_kind IS '内容来源：file 上传原件、text 在线编写、web 网页导入';
COMMENT ON COLUMN knowledge_documents.file_id IS '原件编号及上传重试幂等键，非上传来源为空';
COMMENT ON COLUMN knowledge_documents.title IS '在线文档与网页文档的名称';
COMMENT ON COLUMN knowledge_documents.source_url IS '网页文档的页面地址';
COMMENT ON COLUMN knowledge_documents.status IS '文档处理状态';
COMMENT ON COLUMN knowledge_documents.processing_id IS '当前处理任务的幂等标识';
COMMENT ON COLUMN knowledge_documents.segment_batch_id IS '已发布分段批次编号';
COMMENT ON COLUMN knowledge_documents.segment_count IS '已发布分段数量';
COMMENT ON COLUMN knowledge_documents.failure_code IS '处理失败原因码';
COMMENT ON COLUMN knowledge_documents.chunk_length IS '任务分段长度（字符数）';
COMMENT ON COLUMN knowledge_documents.chunk_overlap IS '任务分段重叠长度（字符数）';
COMMENT ON COLUMN knowledge_documents.created_by_user_id IS '创建用户编号';

-- +goose Down
DROP TABLE knowledge_documents;

-- +goose Up
-- 创建知识问答条目表。
CREATE TABLE knowledge_qa_entries (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    knowledge_base_id   uuid NOT NULL,
    status              varchar(32) NOT NULL DEFAULT 'initial'::varchar,
    processing_id       uuid,
    segment_batch_id    uuid,
    segment_count       integer NOT NULL DEFAULT 0,
    failure_code        varchar(64) NOT NULL DEFAULT ''::varchar,
    created_by_user_id  uuid NOT NULL
);

CREATE INDEX knowledge_qa_entries_knowledge_base_idx
    ON knowledge_qa_entries (knowledge_base_id);

CREATE TRIGGER knowledge_qa_entries_set_updated_at BEFORE UPDATE ON knowledge_qa_entries FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE knowledge_qa_entries IS '知识问答条目';
COMMENT ON COLUMN knowledge_qa_entries.id IS '问答编号';
COMMENT ON COLUMN knowledge_qa_entries.created_at IS '创建时间';
COMMENT ON COLUMN knowledge_qa_entries.updated_at IS '更新时间';
COMMENT ON COLUMN knowledge_qa_entries.knowledge_base_id IS '所属知识库编号';
COMMENT ON COLUMN knowledge_qa_entries.status IS '问答索引状态';
COMMENT ON COLUMN knowledge_qa_entries.processing_id IS '当前索引任务的幂等标识';
COMMENT ON COLUMN knowledge_qa_entries.segment_batch_id IS '已发布分段批次编号';
COMMENT ON COLUMN knowledge_qa_entries.segment_count IS '已发布分段数量';
COMMENT ON COLUMN knowledge_qa_entries.failure_code IS '索引失败原因码';
COMMENT ON COLUMN knowledge_qa_entries.created_by_user_id IS '创建用户编号';

-- +goose Down
DROP TABLE knowledge_qa_entries;

-- +goose Up
-- 创建知识问答内容表。
CREATE TABLE knowledge_qa_contents (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    entry_id    uuid NOT NULL,
    kind        text NOT NULL,
    content     text NOT NULL,
    sort_order  integer NOT NULL DEFAULT 0
);

CREATE INDEX knowledge_qa_contents_entry_idx
    ON knowledge_qa_contents (entry_id, sort_order);

CREATE UNIQUE INDEX knowledge_qa_contents_single_kind_unique
    ON knowledge_qa_contents (entry_id, kind) WHERE (kind = ANY (ARRAY['primary_question'::text, 'answer'::text]));

CREATE TRIGGER knowledge_qa_contents_set_updated_at BEFORE UPDATE ON knowledge_qa_contents FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE knowledge_qa_contents IS '知识问答内容';
COMMENT ON COLUMN knowledge_qa_contents.id IS '内容编号';
COMMENT ON COLUMN knowledge_qa_contents.created_at IS '创建时间';
COMMENT ON COLUMN knowledge_qa_contents.updated_at IS '更新时间';
COMMENT ON COLUMN knowledge_qa_contents.entry_id IS '所属问答编号';
COMMENT ON COLUMN knowledge_qa_contents.kind IS '内容类型：primary_question 主问题、similar_question 相似问题、answer 答案';
COMMENT ON COLUMN knowledge_qa_contents.content IS '内容正文';
COMMENT ON COLUMN knowledge_qa_contents.sort_order IS '相似问题展示顺序';

-- +goose Down
DROP TABLE knowledge_qa_contents;

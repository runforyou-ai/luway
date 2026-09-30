-- +goose Up
-- 创建知识文档正文表。
CREATE TABLE knowledge_document_contents (
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    document_id  uuid PRIMARY KEY,
    content      text NOT NULL
);

COMMENT ON TABLE knowledge_document_contents IS '知识文档正文，保存在线编写内容与网页抓取快照';
COMMENT ON COLUMN knowledge_document_contents.created_at IS '创建时间';
COMMENT ON COLUMN knowledge_document_contents.updated_at IS '更新时间';
COMMENT ON COLUMN knowledge_document_contents.document_id IS '所属文档编号';
COMMENT ON COLUMN knowledge_document_contents.content IS '文档正文的 Markdown 内容';

-- +goose Down
DROP TABLE knowledge_document_contents;

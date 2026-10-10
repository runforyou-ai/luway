-- +goose Up
-- 创建网站渠道帮助中心发布的知识库表。
CREATE TABLE website_channel_knowledge_bases (
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    channel_id         uuid NOT NULL,
    knowledge_base_id  uuid NOT NULL,
    workspace_id       uuid NOT NULL,
    PRIMARY KEY (channel_id, knowledge_base_id)
);

CREATE TRIGGER website_channel_knowledge_bases_set_updated_at BEFORE UPDATE ON website_channel_knowledge_bases FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE website_channel_knowledge_bases IS '网站渠道帮助中心发布的知识库';
COMMENT ON COLUMN website_channel_knowledge_bases.created_at IS '创建时间';
COMMENT ON COLUMN website_channel_knowledge_bases.updated_at IS '更新时间';
COMMENT ON COLUMN website_channel_knowledge_bases.channel_id IS '网站渠道编号';
COMMENT ON COLUMN website_channel_knowledge_bases.knowledge_base_id IS '发布到帮助中心的知识库编号';
COMMENT ON COLUMN website_channel_knowledge_bases.workspace_id IS '所属工作区编号';

-- +goose Down
DROP TABLE website_channel_knowledge_bases;

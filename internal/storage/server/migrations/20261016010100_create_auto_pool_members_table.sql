-- +goose Up
-- 创建平台 Auto 能力池成员表：用户侧只选 Auto，调用时按用途从本表加权选型。
CREATE TABLE auto_pool_members (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    provider_id         uuid NOT NULL,
    identifier          text NOT NULL,
    model_type          text NOT NULL,
    input_modalities    jsonb NOT NULL,
    context_window      bigint NOT NULL,
    max_output_tokens   bigint NOT NULL,
    priority            integer NOT NULL DEFAULT 0,
    weight              integer NOT NULL DEFAULT 1,
    enabled             boolean NOT NULL DEFAULT true
);

ALTER TABLE auto_pool_members
    ADD CONSTRAINT auto_pool_members_provider_identifier_unique UNIQUE (provider_id, identifier) DEFERRABLE INITIALLY DEFERRED;

CREATE INDEX auto_pool_members_type_enabled_idx
    ON auto_pool_members (model_type, enabled);

CREATE TRIGGER auto_pool_members_set_updated_at BEFORE UPDATE ON auto_pool_members FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE auto_pool_members IS '平台 Auto 能力池成员，每条对应一个上游；业务配置引用 Auto，调用时按模型类型与权重选型';
COMMENT ON COLUMN auto_pool_members.id IS '池成员编号';
COMMENT ON COLUMN auto_pool_members.created_at IS '添加时间';
COMMENT ON COLUMN auto_pool_members.updated_at IS '更新时间';
COMMENT ON COLUMN auto_pool_members.provider_id IS '平台供应商编号';
COMMENT ON COLUMN auto_pool_members.identifier IS '上游模型标识';
COMMENT ON COLUMN auto_pool_members.model_type IS '模型类型：chat 对话、embedding 向量、rerank 重排、decision 判断';
COMMENT ON COLUMN auto_pool_members.input_modalities IS '支持的输入模态';
COMMENT ON COLUMN auto_pool_members.context_window IS '上下文窗口 Token 数';
COMMENT ON COLUMN auto_pool_members.max_output_tokens IS '对话模型最大输出 Token 数，其他类型为 0';
COMMENT ON COLUMN auto_pool_members.priority IS '备用顺序，数值小的在前；权重为 0 的成员按此顺序尝试';
COMMENT ON COLUMN auto_pool_members.weight IS '被选为先尝试成员的相对权重，0 表示只作备用';
COMMENT ON COLUMN auto_pool_members.enabled IS '是否参与 Auto 选型';

-- +goose Down
DROP TABLE auto_pool_members;

-- +goose Up
-- 创建服务会话表。
CREATE TABLE service_conversations (
    id                          uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    organization_id             uuid NOT NULL,
    conversation_id             uuid NOT NULL,
    source                      text NOT NULL,
    requester_subject_id        uuid NOT NULL,
    audience                    text NOT NULL,
    current_service_session_id  uuid
);

CREATE UNIQUE INDEX service_conversations_organization_conversation_unique
    ON service_conversations (organization_id, conversation_id);

COMMENT ON TABLE service_conversations IS '服务会话：会话中由组织负责处理发起人请求的部分';
COMMENT ON COLUMN service_conversations.id IS '服务会话编号';
COMMENT ON COLUMN service_conversations.created_at IS '创建时间';
COMMENT ON COLUMN service_conversations.updated_at IS '更新时间';
COMMENT ON COLUMN service_conversations.organization_id IS '所属工作区编号';
COMMENT ON COLUMN service_conversations.conversation_id IS '承载服务会话的会话编号';
COMMENT ON COLUMN service_conversations.source IS '来源：channel 渠道、direct 单聊';
COMMENT ON COLUMN service_conversations.requester_subject_id IS '发起人聊天主体编号';
COMMENT ON COLUMN service_conversations.audience IS '发起人所属服务对象：customer 客户、employee 员工、partner 伙伴';
COMMENT ON COLUMN service_conversations.current_service_session_id IS '当前服务周期编号';
COMMENT ON INDEX service_conversations_organization_conversation_unique IS '工作区会话服务会话唯一索引';

-- +goose Down
DROP TABLE service_conversations;

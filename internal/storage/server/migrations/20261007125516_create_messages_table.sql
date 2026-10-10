-- +goose Up
-- 创建按编号月份分区的会话消息表，分区由服务端启动与每日维护任务创建。
CREATE EXTENSION IF NOT EXISTS btree_gin;

CREATE TABLE messages (
    id                     uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    workspace_id           uuid NOT NULL,
    conversation_id        uuid NOT NULL,
    service_session_id     uuid,
    sender_participant_id  uuid,
    type                   text NOT NULL,
    body                   text NOT NULL DEFAULT '',
    reply_to_message_id    uuid,
    idempotency_key        text,
    originated_at          timestamptz NOT NULL,
    edited_at              timestamptz,
    deleted_at             timestamptz,
    system_event_type      text,
    system_event_payload   jsonb,
    mention_all            boolean NOT NULL DEFAULT false,
    message_seq            bigint NOT NULL,
    client_message_id      uuid,
    visibility             text NOT NULL DEFAULT 'shared',
    search_vector          tsvector NOT NULL DEFAULT ''::tsvector,
    language               text,
    agent_tool_call_id     uuid
) PARTITION BY RANGE (id);

CREATE INDEX messages_conversation_seq_idx
    ON messages (workspace_id, conversation_id, message_seq);

CREATE INDEX messages_idempotency_idx
    ON messages (workspace_id, idempotency_key) WHERE (idempotency_key IS NOT NULL);

CREATE INDEX messages_search_vector_idx
    ON messages USING gin (workspace_id, search_vector);

CREATE INDEX messages_service_session_idx
    ON messages (workspace_id, service_session_id, message_seq) WHERE (service_session_id IS NOT NULL);

ALTER TABLE messages ALTER COLUMN search_vector SET STATISTICS 3000;

CREATE TRIGGER messages_set_updated_at BEFORE UPDATE ON messages FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE messages IS '会话消息，按编号所含的创建月份分区';
COMMENT ON COLUMN messages.id IS '消息编号，UUIDv7，分区键';
COMMENT ON COLUMN messages.created_at IS '创建时间';
COMMENT ON COLUMN messages.updated_at IS '更新时间';
COMMENT ON COLUMN messages.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN messages.conversation_id IS '所属会话编号';
COMMENT ON COLUMN messages.service_session_id IS '所属服务周期编号';
COMMENT ON COLUMN messages.sender_participant_id IS '发送参与者编号';
COMMENT ON COLUMN messages.type IS '消息类型：text 文本、system 系统事件、agent_error AI 运行失败、attachment 附件';
COMMENT ON COLUMN messages.body IS '消息文本内容';
COMMENT ON COLUMN messages.reply_to_message_id IS '回复目标消息编号';
COMMENT ON COLUMN messages.idempotency_key IS '消息写入幂等标识';
COMMENT ON COLUMN messages.originated_at IS '消息在来源端发生时间';
COMMENT ON COLUMN messages.edited_at IS '最后编辑时间';
COMMENT ON COLUMN messages.deleted_at IS '删除时间';
COMMENT ON COLUMN messages.system_event_type IS '系统事件类型';
COMMENT ON COLUMN messages.system_event_payload IS '系统事件的类型化审计载荷';
COMMENT ON COLUMN messages.mention_all IS '是否提醒群聊中的所有成员';
COMMENT ON COLUMN messages.message_seq IS '会话内的消息写入顺序';
COMMENT ON COLUMN messages.client_message_id IS '发送方客户端消息编号';
COMMENT ON COLUMN messages.visibility IS '消息可见范围：shared 会话各方可见，internal 仅服务会话的处理方可见，requester 仅工作区成员发起人可见';
COMMENT ON COLUMN messages.search_vector IS '消息检索词元：正文与附件文件名的单字、字母数字片段和汉字全拼读音';
COMMENT ON COLUMN messages.language IS '正文语言，BCP 47 语言标签，无语言内容时为 und；尚未识别时为空';
COMMENT ON COLUMN messages.agent_tool_call_id IS '产生该消息的工具调用编号，本机 Agent 的回复关联委派的一轮';
COMMENT ON INDEX messages_conversation_seq_idx IS '会话内消息序号索引，序号唯一性由会话行锁保证';
COMMENT ON INDEX messages_idempotency_idx IS '工作区消息幂等标识索引，幂等性由写入时持有的会话行锁保证';
COMMENT ON INDEX messages_search_vector_idx IS '按工作区过滤的消息检索词元索引';

-- +goose Down
DROP TABLE messages;

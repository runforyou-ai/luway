-- +goose Up
-- 创建用户会话状态表。
CREATE TABLE conversation_user_states (
    id                                uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at                        timestamptz NOT NULL DEFAULT now(),
    updated_at                        timestamptz NOT NULL DEFAULT now(),
    organization_id                   uuid NOT NULL,
    conversation_id                   uuid NOT NULL,
    user_id                           uuid NOT NULL,
    last_read_message_id              uuid,
    last_read_at                      timestamptz,
    muted                             boolean NOT NULL DEFAULT false,
    last_reviewed_mention_message_id  uuid,
    marked_unread                     boolean NOT NULL DEFAULT false,
    read_seq                          bigint NOT NULL DEFAULT 0,
    pin_rank                          bigint,
    version                           bigint NOT NULL DEFAULT 0,
    archived_at                       timestamptz
);

ALTER TABLE conversation_user_states
    ADD CONSTRAINT conversation_user_states_user_pin_rank_unique UNIQUE (organization_id, user_id, pin_rank) DEFERRABLE INITIALLY DEFERRED;

CREATE UNIQUE INDEX conversation_user_states_org_conversation_user_unique
    ON conversation_user_states (organization_id, conversation_id, user_id);

CREATE INDEX conversation_user_states_organization_user_index
    ON conversation_user_states (organization_id, user_id, conversation_id);

COMMENT ON TABLE conversation_user_states IS '用户会话个人状态';
COMMENT ON COLUMN conversation_user_states.id IS '用户会话状态编号';
COMMENT ON COLUMN conversation_user_states.created_at IS '创建时间';
COMMENT ON COLUMN conversation_user_states.updated_at IS '更新时间';
COMMENT ON COLUMN conversation_user_states.organization_id IS '所属工作区编号';
COMMENT ON COLUMN conversation_user_states.conversation_id IS '会话编号';
COMMENT ON COLUMN conversation_user_states.user_id IS '用户账号编号';
COMMENT ON COLUMN conversation_user_states.last_read_message_id IS '最后已读消息编号，尚未产生已读水位时为空';
COMMENT ON COLUMN conversation_user_states.last_read_at IS '最后标记已读时间，尚未产生已读水位时为空';
COMMENT ON COLUMN conversation_user_states.muted IS '是否降低当前用户在会话中的消息提醒';
COMMENT ON COLUMN conversation_user_states.last_reviewed_mention_message_id IS '已连续查看的提及或本轮入群基线消息编号';
COMMENT ON COLUMN conversation_user_states.marked_unread IS '用户主动设置的独立未读标记';
COMMENT ON COLUMN conversation_user_states.read_seq IS '用户已阅读的会话消息序号';
COMMENT ON COLUMN conversation_user_states.pin_rank IS '个人置顶顺序值，按升序排列，未置顶时为空';
COMMENT ON COLUMN conversation_user_states.version IS '用户会话个人状态版本，已读、提及确认、静音和手动未读实际变化时推进';
COMMENT ON INDEX conversation_user_states_org_conversation_user_unique IS '工作区会话用户状态唯一索引';
COMMENT ON INDEX conversation_user_states_organization_user_index IS '工作区用户会话状态查询索引';
COMMENT ON INDEX conversation_user_states_user_pin_rank_unique IS '工作区用户置顶顺序值唯一约束';

COMMENT ON COLUMN conversation_user_states.archived_at IS '个人归档时间，会话出现新的对话消息时清空，未归档时为空';

-- +goose Down
DROP TABLE conversation_user_states;

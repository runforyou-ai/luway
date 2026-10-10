-- +goose Up
-- 创建连续查看水位之后已单独查看的群聊提及表。
CREATE TABLE conversation_mention_reviews (
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    workspace_id     uuid NOT NULL,
    conversation_id  uuid NOT NULL,
    user_id          uuid NOT NULL,
    message_id       uuid NOT NULL,
    PRIMARY KEY (workspace_id, conversation_id, user_id, message_id)
);

CREATE TRIGGER conversation_mention_reviews_set_updated_at BEFORE UPDATE ON conversation_mention_reviews FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE conversation_mention_reviews IS '连续查看水位之后已单独查看的群聊提及';
COMMENT ON COLUMN conversation_mention_reviews.created_at IS '创建时间';
COMMENT ON COLUMN conversation_mention_reviews.updated_at IS '更新时间';
COMMENT ON COLUMN conversation_mention_reviews.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN conversation_mention_reviews.conversation_id IS '群聊编号';
COMMENT ON COLUMN conversation_mention_reviews.user_id IS '查看用户编号';
COMMENT ON COLUMN conversation_mention_reviews.message_id IS '已查看提及消息编号';

-- +goose Down
DROP TABLE conversation_mention_reviews;

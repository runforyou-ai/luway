-- +goose Up
-- 创建消息译文表，每条消息每种语言至多一份译文。
CREATE TABLE message_translations (
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    message_id    uuid NOT NULL,
    language      text NOT NULL,
    workspace_id  uuid NOT NULL,
    body          text NOT NULL,
    authored      boolean NOT NULL DEFAULT false,
    PRIMARY KEY (message_id, language)
);

CREATE TRIGGER message_translations_set_updated_at BEFORE UPDATE ON message_translations FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE message_translations IS '客户会话消息的译文；客服翻译发送时，客服书写的原话按客服语言保存为该消息的一份译文';
COMMENT ON COLUMN message_translations.created_at IS '创建时间';
COMMENT ON COLUMN message_translations.updated_at IS '更新时间';
COMMENT ON COLUMN message_translations.message_id IS '消息编号';
COMMENT ON COLUMN message_translations.language IS '译文语言，BCP 47 语言标签';
COMMENT ON COLUMN message_translations.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN message_translations.body IS '译文正文';
COMMENT ON COLUMN message_translations.authored IS '是否为翻译发送时客服书写的原话，每条消息至多一份';

-- +goose Down
DROP TABLE message_translations;

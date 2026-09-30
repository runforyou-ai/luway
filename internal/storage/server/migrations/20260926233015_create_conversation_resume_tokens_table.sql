-- +goose Up
-- 创建客户会话回访令牌表，邮件中的「继续对话」链接凭令牌回到原会话。
CREATE TABLE conversation_resume_tokens (
    id                           uuid PRIMARY KEY,
    created_at                   timestamptz NOT NULL DEFAULT now(),
    updated_at                   timestamptz NOT NULL DEFAULT now(),
    organization_id              uuid NOT NULL,
    conversation_id              uuid NOT NULL,
    contact_channel_identity_id  uuid NOT NULL,
    token_hash                   text NOT NULL,
    expires_at                   timestamptz NOT NULL
);

CREATE UNIQUE INDEX conversation_resume_tokens_token_hash_unique
    ON conversation_resume_tokens (token_hash);

COMMENT ON TABLE conversation_resume_tokens IS '客户会话回访令牌；有效期内可重复换取该渠道身份的访客凭据并打开对应会话';
COMMENT ON COLUMN conversation_resume_tokens.id IS '令牌记录编号';
COMMENT ON COLUMN conversation_resume_tokens.created_at IS '创建时间';
COMMENT ON COLUMN conversation_resume_tokens.updated_at IS '更新时间';
COMMENT ON COLUMN conversation_resume_tokens.organization_id IS '所属工作区编号';
COMMENT ON COLUMN conversation_resume_tokens.conversation_id IS '回访的客户会话编号';
COMMENT ON COLUMN conversation_resume_tokens.contact_channel_identity_id IS '令牌绑定的渠道身份编号';
COMMENT ON COLUMN conversation_resume_tokens.token_hash IS '令牌原文的 SHA-256 十六进制摘要';
COMMENT ON COLUMN conversation_resume_tokens.expires_at IS '令牌失效时间';

-- +goose Down
DROP TABLE conversation_resume_tokens;

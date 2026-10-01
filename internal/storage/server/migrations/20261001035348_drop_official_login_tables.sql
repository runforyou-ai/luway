-- +goose Up
-- 删除官方账号登录使用的外部身份绑定表和登录尝试表。
DROP TABLE login_attempts;
DROP TABLE account_external_identities;

-- +goose Down
-- 创建账号外部身份绑定表。
CREATE TABLE account_external_identities (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    account_id  uuid NOT NULL,
    issuer      text NOT NULL,
    subject     text NOT NULL
);

CREATE UNIQUE INDEX account_external_identities_account_issuer_unique
    ON account_external_identities (account_id, issuer);

CREATE UNIQUE INDEX account_external_identities_issuer_subject_unique
    ON account_external_identities (issuer, subject);

COMMENT ON TABLE account_external_identities IS '账号与官方身份服务账号的绑定';
COMMENT ON COLUMN account_external_identities.id IS '绑定编号';
COMMENT ON COLUMN account_external_identities.created_at IS '创建时间';
COMMENT ON COLUMN account_external_identities.updated_at IS '更新时间';
COMMENT ON COLUMN account_external_identities.account_id IS '账号编号';
COMMENT ON COLUMN account_external_identities.issuer IS '官方身份服务 issuer';
COMMENT ON COLUMN account_external_identities.subject IS '官方身份服务账号的稳定标识';

-- 创建官方账号登录尝试表。
CREATE TABLE login_attempts (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    purpose         text NOT NULL,
    client_type     text NOT NULL,
    redirect_uri    text NOT NULL,
    code_challenge  text NOT NULL,
    nonce           text NOT NULL,
    expires_at      timestamptz NOT NULL,
    consumed_at     timestamptz
);

COMMENT ON TABLE login_attempts IS '官方账号登录尝试，授权码交换时一次性消费';
COMMENT ON COLUMN login_attempts.id IS '登录尝试编号';
COMMENT ON COLUMN login_attempts.created_at IS '创建时间';
COMMENT ON COLUMN login_attempts.updated_at IS '更新时间';
COMMENT ON COLUMN login_attempts.purpose IS '用途：login';
COMMENT ON COLUMN login_attempts.client_type IS '发起登录的客户端类型：web';
COMMENT ON COLUMN login_attempts.redirect_uri IS '授权请求使用的回调地址';
COMMENT ON COLUMN login_attempts.code_challenge IS 'PKCE S256 challenge';
COMMENT ON COLUMN login_attempts.nonce IS '写入 ID Token 的 nonce';
COMMENT ON COLUMN login_attempts.expires_at IS '过期时间';
COMMENT ON COLUMN login_attempts.consumed_at IS '授权码交换消费时间，为空表示尚未使用';

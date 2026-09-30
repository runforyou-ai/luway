-- +goose Up
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

-- +goose Down
DROP TABLE account_external_identities;

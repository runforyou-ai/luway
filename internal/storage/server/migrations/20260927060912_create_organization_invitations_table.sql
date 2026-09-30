-- +goose Up
-- 创建工作区成员邀请表。
CREATE TABLE organization_invitations (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    organization_id      uuid NOT NULL,
    invited_email        text NOT NULL,
    display_name         text NOT NULL,
    role_id              uuid NOT NULL,
    token_hash           text NOT NULL,
    status               text NOT NULL DEFAULT 'pending',
    expires_at           timestamptz NOT NULL,
    invited_by_user_id   uuid NOT NULL,
    accepted_account_id  uuid,
    accepted_user_id     uuid,
    accepted_at          timestamptz
);

CREATE UNIQUE INDEX organization_invitations_token_hash_unique
    ON organization_invitations (token_hash);

CREATE UNIQUE INDEX organization_invitations_pending_email_unique
    ON organization_invitations (organization_id, lower(invited_email))
    WHERE status = 'pending';

COMMENT ON TABLE organization_invitations IS '工作区成员邀请';
COMMENT ON COLUMN organization_invitations.id IS '邀请编号';
COMMENT ON COLUMN organization_invitations.created_at IS '创建时间';
COMMENT ON COLUMN organization_invitations.updated_at IS '更新时间';
COMMENT ON COLUMN organization_invitations.organization_id IS '所属工作区编号';
COMMENT ON COLUMN organization_invitations.invited_email IS '受邀邮箱，规范化存储';
COMMENT ON COLUMN organization_invitations.display_name IS '加入后在工作区中的显示名称';
COMMENT ON COLUMN organization_invitations.role_id IS '加入后的工作区角色编号';
COMMENT ON COLUMN organization_invitations.token_hash IS '邀请令牌摘要，明文只在创建时返回一次';
COMMENT ON COLUMN organization_invitations.status IS '邀请状态：pending、accepted、revoked、expired';
COMMENT ON COLUMN organization_invitations.expires_at IS '过期时间';
COMMENT ON COLUMN organization_invitations.invited_by_user_id IS '发起邀请的成员编号';
COMMENT ON COLUMN organization_invitations.accepted_account_id IS '接受邀请的账号编号';
COMMENT ON COLUMN organization_invitations.accepted_user_id IS '接受邀请后创建的成员编号';
COMMENT ON COLUMN organization_invitations.accepted_at IS '接受时间';

-- +goose Down
DROP TABLE organization_invitations;

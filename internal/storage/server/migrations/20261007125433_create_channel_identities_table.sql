-- +goose Up
-- 创建渠道身份表。
CREATE TABLE channel_identities (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    workspace_id       uuid NOT NULL,
    contact_id         uuid,
    channel_id         uuid NOT NULL,
    external_id        text NOT NULL,
    display_name       text,
    avatar_file_id     uuid,
    last_seen_at       timestamptz,
    avatar_checked_at  timestamptz,
    verified_user_id   text,
    user_identity_id   uuid
);

CREATE UNIQUE INDEX channel_identities_channel_external_unique
    ON channel_identities (channel_id, external_id);

CREATE UNIQUE INDEX channel_identities_channel_user_unique
    ON channel_identities (channel_id, user_identity_id) WHERE (user_identity_id IS NOT NULL);

CREATE INDEX channel_identities_contact_idx
    ON channel_identities (workspace_id, contact_id) WHERE (contact_id IS NOT NULL);

CREATE INDEX channel_identities_external_idx
    ON channel_identities (workspace_id, external_id);

CREATE TRIGGER channel_identities_set_updated_at BEFORE UPDATE ON channel_identities FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE channel_identities IS '外部平台账号在渠道中的身份';
COMMENT ON COLUMN channel_identities.id IS '渠道身份编号';
COMMENT ON COLUMN channel_identities.created_at IS '创建时间';
COMMENT ON COLUMN channel_identities.updated_at IS '更新时间';
COMMENT ON COLUMN channel_identities.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN channel_identities.contact_id IS '服务客户的渠道中身份所属的联系人编号；服务成员的渠道中为空';
COMMENT ON COLUMN channel_identities.channel_id IS '渠道编号';
COMMENT ON COLUMN channel_identities.external_id IS '外部平台账号在渠道中的编号';
COMMENT ON COLUMN channel_identities.display_name IS '外部平台账号在渠道中的显示名称';
COMMENT ON COLUMN channel_identities.avatar_file_id IS '渠道头像文件编号';
COMMENT ON COLUMN channel_identities.last_seen_at IS '最后活跃时间';
COMMENT ON COLUMN channel_identities.avatar_checked_at IS '最近一次发起渠道头像同步的时间，从未同步时为空';
COMMENT ON COLUMN channel_identities.verified_user_id IS '经签名身份核验的企业用户编号，与所属联系人的企业用户编号一致；未核验时为空';
COMMENT ON COLUMN channel_identities.user_identity_id IS '服务成员的渠道中身份绑定的成员企业身份编号，未绑定时为空；服务客户的渠道中为空';

-- +goose Down
DROP TABLE channel_identities;

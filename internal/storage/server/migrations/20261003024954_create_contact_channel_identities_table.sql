-- +goose Up
-- 创建联系人渠道身份表。
CREATE TABLE contact_channel_identities (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    organization_id    uuid NOT NULL,
    contact_id         uuid NOT NULL,
    channel_id         uuid NOT NULL,
    external_id        text NOT NULL,
    display_name       text,
    avatar_file_id     uuid,
    last_seen_at       timestamptz,
    avatar_checked_at  timestamptz,
    verified_user_id   text
);

CREATE UNIQUE INDEX contact_channel_identities_channel_external_unique
    ON contact_channel_identities (channel_id, external_id);

COMMENT ON TABLE contact_channel_identities IS '联系人在外部渠道中的身份';
COMMENT ON COLUMN contact_channel_identities.id IS '渠道身份编号';
COMMENT ON COLUMN contact_channel_identities.created_at IS '创建时间';
COMMENT ON COLUMN contact_channel_identities.updated_at IS '更新时间';
COMMENT ON COLUMN contact_channel_identities.organization_id IS '所属工作区编号';
COMMENT ON COLUMN contact_channel_identities.contact_id IS '联系人编号';
COMMENT ON COLUMN contact_channel_identities.channel_id IS '渠道编号';
COMMENT ON COLUMN contact_channel_identities.external_id IS '联系人在渠道中的外部编号';
COMMENT ON COLUMN contact_channel_identities.display_name IS '联系人在渠道中的显示名称';
COMMENT ON COLUMN contact_channel_identities.avatar_file_id IS '渠道头像文件编号';
COMMENT ON COLUMN contact_channel_identities.last_seen_at IS '最后活跃时间';
COMMENT ON COLUMN contact_channel_identities.avatar_checked_at IS '最近一次发起渠道头像同步的时间，从未同步时为空';
COMMENT ON COLUMN contact_channel_identities.verified_user_id IS '经签名身份核验的企业用户编号，与所属联系人的企业用户编号一致；未核验时为空';

-- +goose Down
DROP TABLE contact_channel_identities;

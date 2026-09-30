-- +goose Up
-- 创建外部联系人表。
CREATE TABLE contacts (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    organization_id     uuid NOT NULL,
    created_by_user_id  uuid,
    source_channel_id   uuid NOT NULL,
    display_name        text,
    stage               text NOT NULL DEFAULT 'visitor',
    notes               text,
    deleted_at          timestamptz,
    external_user_id    text,
    number             bigint NOT NULL
);

CREATE UNIQUE INDEX contacts_organization_external_user_unique
    ON contacts (organization_id, external_user_id) WHERE (external_user_id IS NOT NULL);

CREATE UNIQUE INDEX contacts_organization_number_unique
    ON contacts (organization_id, number);

COMMENT ON TABLE contacts IS '工作区外部联系人';
COMMENT ON COLUMN contacts.id IS '联系人编号';
COMMENT ON COLUMN contacts.created_at IS '创建时间';
COMMENT ON COLUMN contacts.updated_at IS '更新时间';
COMMENT ON COLUMN contacts.organization_id IS '所属工作区编号';
COMMENT ON COLUMN contacts.created_by_user_id IS '创建用户编号，渠道自动创建时为空';
COMMENT ON COLUMN contacts.source_channel_id IS '来源渠道编号';
COMMENT ON COLUMN contacts.display_name IS '显示名称';
COMMENT ON COLUMN contacts.stage IS '客户阶段';
COMMENT ON COLUMN contacts.notes IS '备注';
COMMENT ON COLUMN contacts.deleted_at IS '移入回收站时间';
COMMENT ON COLUMN contacts.external_user_id IS '企业用户编号，取自验签通过的签名身份 sub；未验证身份的联系人为空';
COMMENT ON COLUMN contacts.number IS '工作区内联系人编号，从 1 开始按创建顺序递增';

-- +goose Down
DROP TABLE contacts;

-- +goose Up
-- 创建联系人字段取值表。
CREATE TABLE contact_field_values (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    organization_id  uuid NOT NULL,
    contact_id       uuid NOT NULL,
    field_id         uuid NOT NULL,
    value            text NOT NULL,
    source           text NOT NULL,
    source_user_id   uuid,
    source_service_session_id uuid,
    source_session_closed_at  timestamptz
);

CREATE UNIQUE INDEX contact_field_values_contact_field_unique
    ON contact_field_values (contact_id, field_id);

COMMENT ON TABLE contact_field_values IS '联系人的自定义字段取值';
COMMENT ON COLUMN contact_field_values.id IS '取值编号';
COMMENT ON COLUMN contact_field_values.created_at IS '创建时间';
COMMENT ON COLUMN contact_field_values.updated_at IS '更新时间';
COMMENT ON COLUMN contact_field_values.organization_id IS '所属工作区编号';
COMMENT ON COLUMN contact_field_values.contact_id IS '联系人编号';
COMMENT ON COLUMN contact_field_values.field_id IS '字段编号';
COMMENT ON COLUMN contact_field_values.value IS '取值：文本原文、十进制数字、YYYY-MM-DD 日期或单选选项编号';
COMMENT ON COLUMN contact_field_values.source IS '取值来源：member 客服填写，ai AI 根据对话填写，website 网站签名身份同步';
COMMENT ON COLUMN contact_field_values.source_user_id IS '来源为客服时的填写人用户编号';
COMMENT ON COLUMN contact_field_values.source_service_session_id IS '来源为 AI 时抽取所依据的客服周期编号';
COMMENT ON COLUMN contact_field_values.source_session_closed_at IS '来源为 AI 时抽取所依据的那次周期关闭时间，AI 只用关闭更晚的周期覆盖取值';

-- +goose Down
DROP TABLE contact_field_values;

-- +goose Up
-- 创建联系人标签分配表。
CREATE TABLE contact_tag_assignments (
    id                         uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    workspace_id               uuid NOT NULL,
    contact_id                 uuid NOT NULL,
    tag_id                     uuid NOT NULL,
    source                     text NOT NULL,
    source_user_id             uuid,
    source_service_session_id  uuid
);

CREATE UNIQUE INDEX contact_tag_assignments_contact_tag_unique
    ON contact_tag_assignments (contact_id, tag_id);

CREATE INDEX contact_tag_assignments_tag_idx
    ON contact_tag_assignments (workspace_id, tag_id);

CREATE TRIGGER contact_tag_assignments_set_updated_at BEFORE UPDATE ON contact_tag_assignments FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE contact_tag_assignments IS '联系人上的标签';
COMMENT ON COLUMN contact_tag_assignments.id IS '分配编号';
COMMENT ON COLUMN contact_tag_assignments.created_at IS '创建时间';
COMMENT ON COLUMN contact_tag_assignments.updated_at IS '更新时间';
COMMENT ON COLUMN contact_tag_assignments.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN contact_tag_assignments.contact_id IS '联系人编号';
COMMENT ON COLUMN contact_tag_assignments.tag_id IS '标签编号';
COMMENT ON COLUMN contact_tag_assignments.source IS '添加来源：member 客服添加，ai AI 根据对话添加，signed_identity 客户签名身份同步';
COMMENT ON COLUMN contact_tag_assignments.source_user_id IS '来源为客服时的添加人用户编号';
COMMENT ON COLUMN contact_tag_assignments.source_service_session_id IS '来源为 AI 时判断所依据的客服周期编号';

-- +goose Down
DROP TABLE contact_tag_assignments;

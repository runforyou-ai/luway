-- +goose Up
-- 创建工作区联系人字段表。
CREATE TABLE contact_fields (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    workspace_id    uuid NOT NULL,
    name            text NOT NULL,
    type            text NOT NULL,
    options         jsonb NOT NULL DEFAULT '[]'::jsonb,
    ai_instruction  text NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX contact_fields_workspace_name_unique
    ON contact_fields (workspace_id, lower(name));

CREATE TRIGGER contact_fields_set_updated_at BEFORE UPDATE ON contact_fields FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE contact_fields IS '工作区自定义的联系人字段';
COMMENT ON COLUMN contact_fields.id IS '字段编号';
COMMENT ON COLUMN contact_fields.created_at IS '创建时间';
COMMENT ON COLUMN contact_fields.updated_at IS '更新时间';
COMMENT ON COLUMN contact_fields.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN contact_fields.name IS '字段名称，工作区内唯一';
COMMENT ON COLUMN contact_fields.type IS '字段类型：text、number、date、select';
COMMENT ON COLUMN contact_fields.options IS '单选字段的选项，数组元素为 {id, name}；取值保存选项编号';
COMMENT ON COLUMN contact_fields.ai_instruction IS 'AI 填写说明；为空表示 AI 不填写该字段';

-- +goose Down
DROP TABLE contact_fields;

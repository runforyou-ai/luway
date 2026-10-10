-- +goose Up
-- 创建工作区聊天主体表。
CREATE TABLE chat_subjects (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    workspace_id  uuid NOT NULL,
    kind          text NOT NULL,
    source_id     uuid NOT NULL
);

CREATE UNIQUE INDEX chat_subjects_workspace_kind_source_unique
    ON chat_subjects (workspace_id, kind, source_id);

CREATE TRIGGER chat_subjects_set_updated_at BEFORE UPDATE ON chat_subjects FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE chat_subjects IS '工作区聊天主体';
COMMENT ON COLUMN chat_subjects.id IS '聊天主体编号';
COMMENT ON COLUMN chat_subjects.created_at IS '创建时间';
COMMENT ON COLUMN chat_subjects.updated_at IS '更新时间';
COMMENT ON COLUMN chat_subjects.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN chat_subjects.kind IS '聊天主体类型：workspace_identity、contact';
COMMENT ON COLUMN chat_subjects.source_id IS '主体来源记录编号';
COMMENT ON INDEX chat_subjects_workspace_kind_source_unique IS '工作区内聊天主体来源唯一索引';

-- +goose Down
DROP TABLE chat_subjects;

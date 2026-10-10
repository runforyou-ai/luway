-- +goose Up
-- 创建会话共享文件表。
CREATE TABLE conversation_files (
    id                     uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    workspace_id           uuid NOT NULL,
    conversation_id        uuid NOT NULL,
    path                   text NOT NULL,
    file_id                uuid NOT NULL,
    content_hash           text NOT NULL,
    updated_by_subject_id  uuid NOT NULL
);

CREATE UNIQUE INDEX conversation_files_file_unique
    ON conversation_files (file_id);

CREATE UNIQUE INDEX conversation_files_path_unique
    ON conversation_files (workspace_id, conversation_id, path);

CREATE TRIGGER conversation_files_set_updated_at BEFORE UPDATE ON conversation_files FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE conversation_files IS '会话共享文件区中的文件，成员与 AI 员工共用，只保留当前版本';
COMMENT ON COLUMN conversation_files.id IS '会话文件编号';
COMMENT ON COLUMN conversation_files.created_at IS '创建时间';
COMMENT ON COLUMN conversation_files.updated_at IS '更新时间';
COMMENT ON COLUMN conversation_files.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN conversation_files.conversation_id IS '文件区所属会话编号';
COMMENT ON COLUMN conversation_files.path IS '文件区内的相对路径，以 / 分隔目录';
COMMENT ON COLUMN conversation_files.file_id IS '当前版本内容对应的文件编号';
COMMENT ON COLUMN conversation_files.content_hash IS '当前版本内容的 SHA-256 摘要';
COMMENT ON COLUMN conversation_files.updated_by_subject_id IS '最后修改该文件的聊天主体编号：成员或 AI 员工';
COMMENT ON INDEX conversation_files_file_unique IS '一个文件只作为一个会话文件的当前版本';
COMMENT ON INDEX conversation_files_path_unique IS '同一会话文件区内路径唯一';

-- +goose Down
DROP TABLE conversation_files;

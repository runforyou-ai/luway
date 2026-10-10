-- +goose Up
-- 创建工作区身份表。
CREATE TABLE workspace_identities (
    id                        uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at                timestamptz NOT NULL DEFAULT now(),
    updated_at                timestamptz NOT NULL DEFAULT now(),
    workspace_id              uuid NOT NULL,
    type                      text NOT NULL,
    display_name              text NOT NULL,
    avatar_file_id            uuid,
    work_status               text NOT NULL DEFAULT 'working',
    work_status_updated_at    timestamptz NOT NULL DEFAULT now(),
    handles_service_requests  boolean NOT NULL DEFAULT false
);

CREATE TRIGGER workspace_identities_set_updated_at BEFORE UPDATE ON workspace_identities FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE workspace_identities IS '工作区身份';
COMMENT ON COLUMN workspace_identities.id IS '身份编号';
COMMENT ON COLUMN workspace_identities.created_at IS '创建时间';
COMMENT ON COLUMN workspace_identities.updated_at IS '更新时间';
COMMENT ON COLUMN workspace_identities.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN workspace_identities.type IS '身份类型';
COMMENT ON COLUMN workspace_identities.display_name IS '显示名称';
COMMENT ON COLUMN workspace_identities.avatar_file_id IS '头像文件编号';
COMMENT ON COLUMN workspace_identities.work_status IS '工作状态';
COMMENT ON COLUMN workspace_identities.work_status_updated_at IS '工作状态更新时间';
COMMENT ON COLUMN workspace_identities.handles_service_requests IS '真人成员是否处理服务请求';

-- +goose Down
DROP TABLE workspace_identities;

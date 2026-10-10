-- +goose Up
-- 创建工作区成员表。
CREATE TABLE users (
    id                             uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at                     timestamptz NOT NULL DEFAULT now(),
    updated_at                     timestamptz NOT NULL DEFAULT now(),
    workspace_id                   uuid NOT NULL,
    account_id                     uuid NOT NULL,
    identity_id                    uuid NOT NULL,
    role_id                        uuid NOT NULL,
    status                         text NOT NULL DEFAULT 'active',
    message_notifications_enabled  boolean NOT NULL DEFAULT true,
    profile_version                bigint NOT NULL DEFAULT 0,
    pin_order_version              bigint NOT NULL DEFAULT 0,
    max_service_sessions           integer NOT NULL DEFAULT 10,
    last_service_assigned_at       timestamptz,
    translation_language           text
);

CREATE INDEX users_account_idx
    ON users (account_id);

CREATE UNIQUE INDEX users_workspace_account_unique
    ON users (workspace_id, account_id);

CREATE UNIQUE INDEX users_workspace_identity_unique
    ON users (workspace_id, identity_id);

CREATE TRIGGER users_set_updated_at BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE users IS '工作区成员';
COMMENT ON COLUMN users.id IS '成员编号';
COMMENT ON COLUMN users.created_at IS '创建时间';
COMMENT ON COLUMN users.updated_at IS '更新时间';
COMMENT ON COLUMN users.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN users.account_id IS '所属账号编号';
COMMENT ON COLUMN users.identity_id IS '工作区身份编号';
COMMENT ON COLUMN users.role_id IS '工作区角色编号';
COMMENT ON COLUMN users.status IS '成员状态：active、inactive';
COMMENT ON COLUMN users.message_notifications_enabled IS '是否启用新消息提醒';
COMMENT ON COLUMN users.profile_version IS '成员身份资料与偏好版本，登录态可见字段实际变化时推进';
COMMENT ON COLUMN users.pin_order_version IS '个人置顶顺序版本，置顶、取消置顶与调整顺序时推进';
COMMENT ON COLUMN users.max_service_sessions IS '最大接待量：自动分配时本人可负责的开放客服处理周期上限';
COMMENT ON COLUMN users.last_service_assigned_at IS '最近一次被自动分配客服处理周期的时间';
COMMENT ON COLUMN users.translation_language IS '客户消息译文与翻译发送使用的本人语言，BCP 47 语言标签；为空时使用账号界面语言';

-- +goose Down
DROP TABLE users;

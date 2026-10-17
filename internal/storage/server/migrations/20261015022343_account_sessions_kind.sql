-- +goose Up
-- 登录会话区分用户端、平台后台与支援进入；支援会话绑定目标工作区。
ALTER TABLE account_sessions
    ADD COLUMN kind varchar(16) NOT NULL DEFAULT 'user',
    ADD COLUMN workspace_id uuid;

CREATE INDEX account_sessions_kind_account_idx
    ON account_sessions (kind, account_id);

CREATE INDEX account_sessions_support_workspace_idx
    ON account_sessions (workspace_id)
    WHERE kind = 'support';

COMMENT ON COLUMN account_sessions.kind IS '会话种类：user 用户端、admin 平台后台、support 支援进入';
COMMENT ON COLUMN account_sessions.workspace_id IS '支援会话绑定的工作区编号，其他种类为空';

-- +goose Down
DROP INDEX IF EXISTS account_sessions_support_workspace_idx;
DROP INDEX IF EXISTS account_sessions_kind_account_idx;
ALTER TABLE account_sessions
    DROP COLUMN IF EXISTS workspace_id,
    DROP COLUMN IF EXISTS kind;

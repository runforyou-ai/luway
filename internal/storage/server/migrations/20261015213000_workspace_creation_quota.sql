-- +goose Up
-- 自建工作区额度与默认工作区；去掉「谁能创建」策略。

ALTER TABLE workspaces
    ADD COLUMN created_by_account_id uuid REFERENCES accounts (id);

COMMENT ON COLUMN workspaces.created_by_account_id IS '创建该工作区的账号；被邀请加入不写入本列';

-- 将每位管理员中最早加入者回填为创建者（尽最大努力）。
UPDATE workspaces AS w
SET created_by_account_id = src.account_id
FROM (
    SELECT DISTINCT ON (u.workspace_id) u.workspace_id, u.account_id
    FROM users AS u
    JOIN roles AS r ON r.id = u.role_id AND r.workspace_id = u.workspace_id
    WHERE r.kind = 'admin' AND u.status = 'active'
    ORDER BY u.workspace_id, u.created_at ASC, u.id ASC
) AS src
WHERE src.workspace_id = w.id;

ALTER TABLE platforms
    ADD COLUMN workspace_creation_quota integer NOT NULL DEFAULT 0,
    ADD COLUMN default_workspace_id uuid REFERENCES workspaces (id);

COMMENT ON COLUMN platforms.workspace_creation_quota IS '每个账号可自建工作区数量上限；未授权时锁为 0，被邀请加入不占用';
COMMENT ON COLUMN platforms.default_workspace_id IS '安装时创建的默认工作区；开放注册且额度为 0 时新用户进入此工作区';

-- 把最早仍有效的工作区标为默认工作区。新模型下「开放注册 + 额度 0」会把自助注册用户加入默认工作区；
-- 旧版开放注册并不授予成员身份，因此升级时一律把开放注册收成仅邀请，避免悄悄扩大既有工作区访问边界。
UPDATE platforms
SET
    default_workspace_id = COALESCE(
        default_workspace_id,
        (
            SELECT id FROM workspaces
            WHERE lifecycle_status = 'active'
            ORDER BY created_at ASC, id ASC
            LIMIT 1
        )
    ),
    registration_policy = CASE
        WHEN registration_policy = 'open' THEN 'invitation_only'
        ELSE registration_policy
    END;

ALTER TABLE platforms DROP COLUMN workspace_creation_policy;

-- +goose Down
ALTER TABLE platforms ADD COLUMN workspace_creation_policy text NOT NULL DEFAULT 'platform_admin';
UPDATE platforms SET workspace_creation_policy = 'platform_admin';
ALTER TABLE platforms DROP COLUMN default_workspace_id;
ALTER TABLE platforms DROP COLUMN workspace_creation_quota;
ALTER TABLE workspaces DROP COLUMN created_by_account_id;

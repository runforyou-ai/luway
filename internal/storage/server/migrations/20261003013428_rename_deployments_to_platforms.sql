-- +goose Up
-- 部署实例表改名为平台表，实例标识改为服务器标识，实例授权表改名为授权表，部署管理员改为平台管理员，约束名随表名与列名更新。
ALTER TABLE deployments RENAME TO platforms;
ALTER TABLE platforms RENAME CONSTRAINT deployments_pkey TO platforms_pkey;
ALTER INDEX deployments_singleton_unique RENAME TO platforms_singleton_unique;
ALTER TABLE platforms RENAME COLUMN instance_id TO server_id;
ALTER TABLE platforms RENAME COLUMN instance_private_key TO server_private_key;
UPDATE platforms SET workspace_creation_policy = 'platform_admin' WHERE workspace_creation_policy = 'deployment_admin';

ALTER TABLE instance_licenses RENAME TO licenses;
ALTER TABLE licenses RENAME CONSTRAINT instance_licenses_pkey TO licenses_pkey;
ALTER TABLE licenses RENAME COLUMN instance_id TO server_id;

ALTER TABLE accounts RENAME COLUMN is_deployment_admin TO is_platform_admin;

ALTER TABLE platforms RENAME CONSTRAINT deployments_created_at_not_null TO platforms_created_at_not_null;
ALTER TABLE platforms RENAME CONSTRAINT deployments_instance_id_not_null TO platforms_server_id_not_null;
ALTER TABLE platforms RENAME CONSTRAINT deployments_instance_private_key_not_null TO platforms_server_private_key_not_null;
ALTER TABLE platforms RENAME CONSTRAINT deployments_registration_policy_not_null TO platforms_registration_policy_not_null;
ALTER TABLE platforms RENAME CONSTRAINT deployments_statistics_rebuild_pending_not_null TO platforms_statistics_rebuild_pending_not_null;
ALTER TABLE platforms RENAME CONSTRAINT deployments_statistics_time_zone_not_null TO platforms_statistics_time_zone_not_null;
ALTER TABLE platforms RENAME CONSTRAINT deployments_telemetry_enabled_not_null TO platforms_telemetry_enabled_not_null;
ALTER TABLE platforms RENAME CONSTRAINT deployments_updated_at_not_null TO platforms_updated_at_not_null;
ALTER TABLE platforms RENAME CONSTRAINT deployments_workspace_creation_policy_not_null TO platforms_workspace_creation_policy_not_null;
ALTER TABLE licenses RENAME CONSTRAINT instance_licenses_capabilities_not_null TO licenses_capabilities_not_null;
ALTER TABLE licenses RENAME CONSTRAINT instance_licenses_created_at_not_null TO licenses_created_at_not_null;
ALTER TABLE licenses RENAME CONSTRAINT instance_licenses_customer_not_null TO licenses_customer_not_null;
ALTER TABLE licenses RENAME CONSTRAINT instance_licenses_expires_at_not_null TO licenses_expires_at_not_null;
ALTER TABLE licenses RENAME CONSTRAINT instance_licenses_instance_id_not_null TO licenses_server_id_not_null;
ALTER TABLE licenses RENAME CONSTRAINT instance_licenses_issued_at_not_null TO licenses_issued_at_not_null;
ALTER TABLE licenses RENAME CONSTRAINT instance_licenses_license_code_not_null TO licenses_license_code_not_null;
ALTER TABLE licenses RENAME CONSTRAINT instance_licenses_license_id_not_null TO licenses_license_id_not_null;
ALTER TABLE licenses RENAME CONSTRAINT instance_licenses_updated_at_not_null TO licenses_updated_at_not_null;
ALTER TABLE accounts RENAME CONSTRAINT accounts_is_deployment_admin_not_null TO accounts_is_platform_admin_not_null;

COMMENT ON TABLE platforms IS '平台记录与平台级策略，首次安装时写入唯一一行';
COMMENT ON COLUMN platforms.server_id IS '服务器标识，首次安装或重置服务器标识时生成';
COMMENT ON COLUMN platforms.server_private_key IS '服务器签名私钥（Ed25519 种子），用于向 control 证明服务器身份，首次安装或重置服务器标识时生成';
COMMENT ON COLUMN platforms.workspace_creation_policy IS '工作区创建策略：any_account 所有账号可创建，platform_admin 仅平台管理员可创建';
COMMENT ON TABLE licenses IS '平台当前生效的授权，每个服务器标识一行，签发时间更晚的授权码替换当前授权';
COMMENT ON COLUMN licenses.server_id IS '授权绑定的服务器标识';
COMMENT ON TABLE accounts IS '平台内的登录账号';
COMMENT ON COLUMN accounts.email IS '登录与联系邮箱，全平台唯一';
COMMENT ON COLUMN accounts.is_platform_admin IS '是否为平台管理员';
COMMENT ON COLUMN organizations.slug IS '工作区标识，全平台唯一，用于 Web 访问地址';
COMMENT ON COLUMN account_daily_activities.activity_date IS '按平台统计时区划分的日期';
COMMENT ON COLUMN workspace_daily_stats.stat_date IS '按平台统计时区划分的日期';
COMMENT ON COLUMN task_runs.organization_id IS '任务所属工作区编号，平台级任务为空';
COMMENT ON COLUMN ai_models.organization_id IS '所属工作区编号，为空表示平台提供的平台模型';
COMMENT ON TABLE ai_providers IS 'AI 供应商，属于工作区或由平台提供';
COMMENT ON COLUMN ai_providers.organization_id IS '所属工作区编号，为空表示平台提供的平台供应商';

-- +goose Down
COMMENT ON COLUMN ai_providers.organization_id IS '所属工作区编号，为空表示部署提供的平台供应商';
COMMENT ON TABLE ai_providers IS 'AI 供应商，属于工作区或由部署提供';
COMMENT ON COLUMN ai_models.organization_id IS '所属工作区编号，为空表示部署提供的平台模型';
COMMENT ON COLUMN task_runs.organization_id IS '任务所属工作区编号，部署级任务为空';
COMMENT ON COLUMN workspace_daily_stats.stat_date IS '按部署统计时区划分的日期';
COMMENT ON COLUMN account_daily_activities.activity_date IS '按部署统计时区划分的日期';
COMMENT ON COLUMN organizations.slug IS '工作区标识，全部署唯一，用于 Web 访问地址';
COMMENT ON COLUMN accounts.is_platform_admin IS '是否为部署管理员';
COMMENT ON COLUMN accounts.email IS '登录与联系邮箱，全部署唯一';
COMMENT ON TABLE accounts IS '部署内的登录账号';
COMMENT ON COLUMN licenses.server_id IS '授权绑定的实例标识';
COMMENT ON TABLE licenses IS '实例当前生效的授权，每个实例一行，签发时间更晚的授权码替换当前授权';
COMMENT ON COLUMN platforms.workspace_creation_policy IS '工作区创建策略：any_account 所有账号可创建，deployment_admin 仅部署管理员可创建';
COMMENT ON COLUMN platforms.server_private_key IS '实例签名私钥（Ed25519 种子），用于向 control 证明实例身份，首次安装或重置实例时生成';
COMMENT ON COLUMN platforms.server_id IS '实例标识，首次安装或重置实例时生成';
COMMENT ON TABLE platforms IS '部署实例与部署级策略，首次安装时写入唯一一行';

ALTER TABLE platforms RENAME CONSTRAINT platforms_created_at_not_null TO deployments_created_at_not_null;
ALTER TABLE platforms RENAME CONSTRAINT platforms_server_id_not_null TO deployments_instance_id_not_null;
ALTER TABLE platforms RENAME CONSTRAINT platforms_server_private_key_not_null TO deployments_instance_private_key_not_null;
ALTER TABLE platforms RENAME CONSTRAINT platforms_registration_policy_not_null TO deployments_registration_policy_not_null;
ALTER TABLE platforms RENAME CONSTRAINT platforms_statistics_rebuild_pending_not_null TO deployments_statistics_rebuild_pending_not_null;
ALTER TABLE platforms RENAME CONSTRAINT platforms_statistics_time_zone_not_null TO deployments_statistics_time_zone_not_null;
ALTER TABLE platforms RENAME CONSTRAINT platforms_telemetry_enabled_not_null TO deployments_telemetry_enabled_not_null;
ALTER TABLE platforms RENAME CONSTRAINT platforms_updated_at_not_null TO deployments_updated_at_not_null;
ALTER TABLE platforms RENAME CONSTRAINT platforms_workspace_creation_policy_not_null TO deployments_workspace_creation_policy_not_null;
ALTER TABLE licenses RENAME CONSTRAINT licenses_capabilities_not_null TO instance_licenses_capabilities_not_null;
ALTER TABLE licenses RENAME CONSTRAINT licenses_created_at_not_null TO instance_licenses_created_at_not_null;
ALTER TABLE licenses RENAME CONSTRAINT licenses_customer_not_null TO instance_licenses_customer_not_null;
ALTER TABLE licenses RENAME CONSTRAINT licenses_expires_at_not_null TO instance_licenses_expires_at_not_null;
ALTER TABLE licenses RENAME CONSTRAINT licenses_server_id_not_null TO instance_licenses_instance_id_not_null;
ALTER TABLE licenses RENAME CONSTRAINT licenses_issued_at_not_null TO instance_licenses_issued_at_not_null;
ALTER TABLE licenses RENAME CONSTRAINT licenses_license_code_not_null TO instance_licenses_license_code_not_null;
ALTER TABLE licenses RENAME CONSTRAINT licenses_license_id_not_null TO instance_licenses_license_id_not_null;
ALTER TABLE licenses RENAME CONSTRAINT licenses_updated_at_not_null TO instance_licenses_updated_at_not_null;
ALTER TABLE accounts RENAME CONSTRAINT accounts_is_platform_admin_not_null TO accounts_is_deployment_admin_not_null;

ALTER TABLE accounts RENAME COLUMN is_platform_admin TO is_deployment_admin;

ALTER TABLE licenses RENAME COLUMN server_id TO instance_id;
ALTER TABLE licenses RENAME CONSTRAINT licenses_pkey TO instance_licenses_pkey;
ALTER TABLE licenses RENAME TO instance_licenses;

UPDATE platforms SET workspace_creation_policy = 'deployment_admin' WHERE workspace_creation_policy = 'platform_admin';
ALTER TABLE platforms RENAME COLUMN server_private_key TO instance_private_key;
ALTER TABLE platforms RENAME COLUMN server_id TO instance_id;
ALTER INDEX platforms_singleton_unique RENAME TO deployments_singleton_unique;
ALTER TABLE platforms RENAME CONSTRAINT platforms_pkey TO deployments_pkey;
ALTER TABLE platforms RENAME TO deployments;

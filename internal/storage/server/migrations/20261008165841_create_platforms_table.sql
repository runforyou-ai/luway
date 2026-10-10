-- +goose Up
-- 创建平台表。
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE platforms (
    created_at                     timestamptz NOT NULL DEFAULT now(),
    updated_at                     timestamptz NOT NULL DEFAULT now(),
    server_id                      uuid PRIMARY KEY DEFAULT uuidv7(),
    registration_policy            text NOT NULL,
    workspace_creation_policy      text NOT NULL,
    time_zone                      text NOT NULL DEFAULT 'UTC',
    statistics_rebuild_pending     boolean NOT NULL DEFAULT false,
    server_private_key             bytea NOT NULL DEFAULT gen_random_bytes(32),
    telemetry_enabled              boolean NOT NULL DEFAULT true,
    control_synced_at              timestamptz,
    control_failed_at              timestamptz,
    control_error                  text NOT NULL DEFAULT '',
    deployment_name                text NOT NULL DEFAULT '',
    public_url                     text NOT NULL,
    s3_enabled                     boolean NOT NULL DEFAULT false,
    s3_endpoint                    text NOT NULL DEFAULT '',
    s3_public_base_url             text NOT NULL DEFAULT '',
    s3_region                      text NOT NULL DEFAULT '',
    s3_bucket                      text NOT NULL DEFAULT '',
    s3_access_key_id               text NOT NULL DEFAULT '',
    s3_secret_access_key           text NOT NULL DEFAULT '',
    s3_force_path_style            boolean NOT NULL DEFAULT false,
    smtp_host                      text NOT NULL DEFAULT '',
    smtp_port                      integer NOT NULL DEFAULT 587,
    smtp_username                  text NOT NULL DEFAULT '',
    smtp_password                  text NOT NULL DEFAULT '',
    smtp_security                  text NOT NULL DEFAULT 'starttls',
    smtp_from_address              text NOT NULL DEFAULT '',
    brand_names                    jsonb NOT NULL DEFAULT '{}'::jsonb,
    brand_sdk_name                 text NOT NULL DEFAULT '',
    brand_icon                     bytea,
    home_self_host                 boolean NOT NULL DEFAULT false,
    certificate_source             text NOT NULL DEFAULT 'acme',
    certificate                    text NOT NULL DEFAULT '',
    certificate_private_key        text NOT NULL DEFAULT '',
    certificate_expires_at         timestamptz,
    certificate_renewal_error      text NOT NULL DEFAULT '',
    certificate_renewal_failed_at  timestamptz,
    acme_account_key               text NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX platforms_singleton_unique
    ON platforms ((true));

CREATE TRIGGER platforms_set_updated_at BEFORE UPDATE ON platforms FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE platforms IS '平台记录与平台级策略，首次安装时写入唯一一行';
COMMENT ON COLUMN platforms.created_at IS '创建时间，即首次安装时间';
COMMENT ON COLUMN platforms.updated_at IS '更新时间';
COMMENT ON COLUMN platforms.server_id IS '服务器标识，首次安装或重置服务器标识时生成';
COMMENT ON COLUMN platforms.registration_policy IS '注册策略：open 开放注册，invitation_only 仅限受邀邮箱注册';
COMMENT ON COLUMN platforms.workspace_creation_policy IS '工作区创建策略：any_account 所有账号可创建，platform_admin 仅平台管理员可创建';
COMMENT ON COLUMN platforms.time_zone IS '平台时区（IANA），运营数据按此划分日期';
COMMENT ON COLUMN platforms.statistics_rebuild_pending IS '修改平台时区后等待按新时区全部重建运营数据';
COMMENT ON COLUMN platforms.server_private_key IS '服务器签名私钥（Ed25519 种子），用于向 control 证明服务器身份，首次安装或重置服务器标识时生成';
COMMENT ON COLUMN platforms.telemetry_enabled IS '是否向 control 上报运行指标与错误';
COMMENT ON COLUMN platforms.control_synced_at IS '最近一次与 control 同步成功的时间，从未成功时为空';
COMMENT ON COLUMN platforms.control_failed_at IS '最近一次与 control 同步失败的时间，此后同步成功时清空';
COMMENT ON COLUMN platforms.control_error IS '最近一次与 control 同步失败的原因，此后同步成功时清空';
COMMENT ON COLUMN platforms.deployment_name IS '展示给连接者的部署名称，为空时界面展示部署地址';
COMMENT ON COLUMN platforms.public_url IS '部署地址：各端连接和 Web 访问使用的不带路径的完整 HTTP 地址，也是服务端生成对外链接的根地址';
COMMENT ON COLUMN platforms.s3_enabled IS '新文件是否写入 S3 兼容对象存储，关闭时写入各服务器的本地文件目录';
COMMENT ON COLUMN platforms.s3_endpoint IS '对象存储接口地址';
COMMENT ON COLUMN platforms.s3_public_base_url IS '客户端访问对象存储文件使用的公开地址';
COMMENT ON COLUMN platforms.s3_region IS '对象存储区域';
COMMENT ON COLUMN platforms.s3_bucket IS '对象存储桶';
COMMENT ON COLUMN platforms.s3_access_key_id IS '对象存储访问密钥 ID';
COMMENT ON COLUMN platforms.s3_secret_access_key IS '对象存储访问密钥';
COMMENT ON COLUMN platforms.s3_force_path_style IS '是否以路径形式访问存储桶';
COMMENT ON COLUMN platforms.smtp_host IS 'SMTP 主机，为空时关闭邮件发送';
COMMENT ON COLUMN platforms.smtp_port IS 'SMTP 端口';
COMMENT ON COLUMN platforms.smtp_username IS 'SMTP 用户名，为空时不认证';
COMMENT ON COLUMN platforms.smtp_password IS 'SMTP 密码';
COMMENT ON COLUMN platforms.smtp_security IS 'SMTP 加密方式：starttls、tls 或 none';
COMMENT ON COLUMN platforms.smtp_from_address IS '发件邮箱';
COMMENT ON COLUMN platforms.brand_names IS '按界面语言标签覆盖的产品名称，授权授予自定义品牌期间生效';
COMMENT ON COLUMN platforms.brand_sdk_name IS '覆盖的网站嵌入脚本全局对象名，为空时沿用构建品牌，授权授予自定义品牌期间生效';
COMMENT ON COLUMN platforms.brand_icon IS '替换 Web 端网站图标的 PNG 图片，授权授予自定义品牌期间生效';
COMMENT ON COLUMN platforms.home_self_host IS '产品首页提供价格区块时是否展示自部署介绍；不提供价格区块时首页始终展示';
COMMENT ON COLUMN platforms.certificate_source IS '部署地址的 HTTPS 证书来源：acme 为自动签发并续期，upload 为管理员上传';
COMMENT ON COLUMN platforms.certificate IS '直接提供 HTTPS 的服务器使用的 PEM 证书链，为空表示没有证书';
COMMENT ON COLUMN platforms.certificate_private_key IS '证书的 PEM 私钥';
COMMENT ON COLUMN platforms.certificate_expires_at IS '证书到期时间';
COMMENT ON COLUMN platforms.certificate_renewal_error IS '最近一次自动签发失败的原因，签发成功后清空';
COMMENT ON COLUMN platforms.certificate_renewal_failed_at IS '最近一次自动签发失败的时间';
COMMENT ON COLUMN platforms.acme_account_key IS 'ACME 账号的 PEM 私钥，首次自动签发时生成';

-- +goose Down
DROP TABLE platforms;

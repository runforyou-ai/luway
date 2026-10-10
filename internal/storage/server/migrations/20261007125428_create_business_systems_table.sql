-- +goose Up
-- 创建工作区业务系统表。
CREATE TABLE business_systems (
    id                uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    workspace_id      uuid NOT NULL,
    name              text NOT NULL,
    transport         text NOT NULL,
    header_bindings   jsonb NOT NULL DEFAULT '{}'::jsonb,
    tools             jsonb NOT NULL DEFAULT '[]'::jsonb,
    tools_updated_at  timestamptz,
    tools_refresh_id  uuid,
    tools_failure     text NOT NULL DEFAULT '',
    tool_settings     jsonb NOT NULL DEFAULT '{}'::jsonb,
    connection        jsonb NOT NULL,
    credential        jsonb NOT NULL
);

CREATE UNIQUE INDEX business_systems_workspace_name_unique
    ON business_systems (workspace_id, lower(name));

CREATE TRIGGER business_systems_set_updated_at BEFORE UPDATE ON business_systems FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE business_systems IS '工作区业务系统：AI 员工调用的外部系统连接';
COMMENT ON COLUMN business_systems.id IS '业务系统编号';
COMMENT ON COLUMN business_systems.created_at IS '添加时间';
COMMENT ON COLUMN business_systems.updated_at IS '更新时间';
COMMENT ON COLUMN business_systems.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN business_systems.name IS '业务系统名称';
COMMENT ON COLUMN business_systems.transport IS '传输方式：mcp 为 MCP 服务，http 为按 OpenAPI 文档调用的 HTTP 接口';
COMMENT ON COLUMN business_systems.header_bindings IS '连接请求头到可信上下文值的绑定';
COMMENT ON COLUMN business_systems.tools IS '最近成功获取的工具目录，含输入参数定义、特性提示与 HTTP 接口';
COMMENT ON COLUMN business_systems.tools_updated_at IS '工具目录更新时间';
COMMENT ON COLUMN business_systems.tools_refresh_id IS '当前工具更新批次';
COMMENT ON COLUMN business_systems.tools_failure IS '工具更新失败原因码';
COMMENT ON COLUMN business_systems.tool_settings IS '工具名到管理员设置的映射：事实修正、停用与参数绑定';
COMMENT ON COLUMN business_systems.connection IS '连接配置：mcp 项为服务地址与连接方式，http 项为接口根地址与 OpenAPI 文档来源';
COMMENT ON COLUMN business_systems.credential IS '工作区共享的凭据：认证方式与令牌、请求头名称或用户名密码';

-- +goose Down
DROP TABLE business_systems;

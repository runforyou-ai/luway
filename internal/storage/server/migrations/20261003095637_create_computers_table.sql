-- +goose Up
-- 创建电脑表。
CREATE TABLE computers (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    organization_id  uuid NOT NULL,
    kind             text NOT NULL,
    owner_user_id    uuid,
    install_id       text NOT NULL,
    name             text NOT NULL,
    platform         text NOT NULL,
    credential_hash  text NOT NULL,
    capabilities     jsonb NOT NULL DEFAULT '{}'::jsonb,
    max_concurrency  integer NOT NULL DEFAULT 4,
    executor_version text NOT NULL DEFAULT '',
    last_seen_at     timestamptz,
    revoked_at       timestamptz
);

CREATE UNIQUE INDEX computers_organization_owner_install_unique
    ON computers (organization_id, owner_user_id, install_id) NULLS NOT DISTINCT;

COMMENT ON TABLE computers IS '为 AI 员工执行文件、命令与本机 MCP 操作的电脑';
COMMENT ON COLUMN computers.id IS '电脑编号';
COMMENT ON COLUMN computers.created_at IS '注册时间';
COMMENT ON COLUMN computers.updated_at IS '更新时间';
COMMENT ON COLUMN computers.organization_id IS '所属工作区编号';
COMMENT ON COLUMN computers.kind IS '电脑类型：personal 成员个人电脑';
COMMENT ON COLUMN computers.owner_user_id IS '个人电脑的主人编号';
COMMENT ON COLUMN computers.install_id IS '执行器安装标识，同一安装重复注册指向同一电脑';
COMMENT ON COLUMN computers.name IS '电脑名称';
COMMENT ON COLUMN computers.platform IS '电脑操作系统平台';
COMMENT ON COLUMN computers.credential_hash IS '电脑凭据的 SHA-256 摘要，执行器凭此连接服务端，与成员登录会话无关';
COMMENT ON COLUMN computers.capabilities IS '执行器上报的执行能力：命令解释器、托管运行环境、本机 MCP 工具目录与技能目录';
COMMENT ON COLUMN computers.max_concurrency IS '同时执行的操作上限';
COMMENT ON COLUMN computers.executor_version IS '执行器版本';
COMMENT ON COLUMN computers.last_seen_at IS '执行器事件流最近一次在线的时间，超过在线时限即视为离线';
COMMENT ON COLUMN computers.revoked_at IS '撤销时间，非空表示电脑凭据已失效';

-- +goose Down
DROP TABLE computers;

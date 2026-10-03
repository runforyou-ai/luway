-- +goose Up
-- AI 员工运行统一在服务端执行，文件、命令与本机 MCP 操作作为工具调用派发到电脑。
ALTER TABLE agents RENAME COLUMN device_id TO computer_id;
COMMENT ON COLUMN agents.computer_id IS '仅服务负责人本人的 AI 员工使用的电脑编号，其他 AI 员工为空';

ALTER TABLE agent_runs
    DROP COLUMN execution_device_id,
    DROP COLUMN claimed_at,
    DROP COLUMN lease_expires_at;

ALTER TABLE agent_tool_calls
    ADD COLUMN computer_id uuid,
    ADD COLUMN operation jsonb;
COMMENT ON COLUMN agent_tool_calls.computer_id IS '执行调用的电脑编号，在服务端执行的调用为空';
COMMENT ON COLUMN agent_tool_calls.operation IS '电脑操作：派发的原语、参数与会话文件夹，以及电脑上报的文件路径、内容摘要与附带文件，仅电脑执行的调用取值';
COMMENT ON COLUMN agent_tool_calls.status IS '调用状态：queued 待执行、running 执行中、waiting 等待外部结果或电脑领取、succeeded 成功、failed 失败、cancelled 已取消、interrupted 已中断、needs_review 待核对；派发到电脑的调用由电脑领取与上报推进';

ALTER TABLE workspace_daily_stats RENAME COLUMN device_count TO computer_count;

DROP TABLE devices;

-- +goose Down
ALTER TABLE workspace_daily_stats RENAME COLUMN computer_count TO device_count;

CREATE TABLE devices (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    organization_id  uuid NOT NULL,
    user_id          uuid NOT NULL,
    install_id       text NOT NULL,
    name             text NOT NULL,
    platform         text NOT NULL,
    revoked_at       timestamptz,
    work_seq         bigint NOT NULL DEFAULT 0,
    last_seen_at     timestamptz,
    local_agents     jsonb NOT NULL DEFAULT '[]'::jsonb
);

CREATE UNIQUE INDEX devices_organization_user_install_unique
    ON devices (organization_id, user_id, install_id);

COMMENT ON TABLE devices IS '成员注册到工作区的本机设备';
COMMENT ON COLUMN devices.id IS '设备编号';
COMMENT ON COLUMN devices.created_at IS '注册时间';
COMMENT ON COLUMN devices.updated_at IS '更新时间';
COMMENT ON COLUMN devices.organization_id IS '所属工作区编号';
COMMENT ON COLUMN devices.user_id IS '设备主人编号';
COMMENT ON COLUMN devices.install_id IS '客户端安装标识，同一安装重复注册指向同一设备';
COMMENT ON COLUMN devices.name IS '设备名称';
COMMENT ON COLUMN devices.platform IS '设备平台';
COMMENT ON COLUMN devices.revoked_at IS '撤销时间，非空表示该设备当前不受信任';
COMMENT ON COLUMN devices.work_seq IS '设备工作水位，派发或停止该设备执行的运行时递增';
COMMENT ON COLUMN devices.last_seen_at IS '设备最近一次以设备身份访问服务端的时间';
COMMENT ON COLUMN devices.local_agents IS '设备上报的已安装且可用的本机 Agent 种类';

ALTER TABLE agent_tool_calls
    DROP COLUMN computer_id,
    DROP COLUMN operation;
COMMENT ON COLUMN agent_tool_calls.status IS '调用状态：queued 待执行、running 执行中、waiting 等待外部结果、succeeded 成功、failed 失败、cancelled 已取消、interrupted 已中断、needs_review 待核对';

ALTER TABLE agent_runs
    ADD COLUMN execution_device_id uuid,
    ADD COLUMN claimed_at timestamptz,
    ADD COLUMN lease_expires_at timestamptz;
COMMENT ON COLUMN agent_runs.execution_device_id IS '执行设备编号，仅服务负责人本人的 AI 员工的运行为其绑定电脑，其他运行为空';
COMMENT ON COLUMN agent_runs.claimed_at IS '设备领取时间';
COMMENT ON COLUMN agent_runs.lease_expires_at IS '设备当前租约过期时间';

ALTER TABLE agents RENAME COLUMN computer_id TO device_id;
COMMENT ON COLUMN agents.device_id IS '仅服务负责人本人的 AI 员工绑定的电脑编号，其他 AI 员工为空';

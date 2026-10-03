-- +goose Up
-- 创建 Agent 工具调用表。
CREATE TABLE agent_tool_calls (
    id                uuid PRIMARY KEY,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    organization_id   uuid NOT NULL,
    agent_run_id      uuid NOT NULL,
    parent_id         uuid,
    model_call_id     uuid NOT NULL,
    provider_call_id  text NOT NULL,
    name              text NOT NULL,
    source            text NOT NULL,
    mcp_server        text,
    arguments         text NOT NULL,
    replayable        boolean NOT NULL,
    side_effects      boolean NOT NULL,
    status            text NOT NULL,
    result            text,
    error             text,
    evidence          boolean NOT NULL DEFAULT false,
    started_at        timestamptz,
    completed_at      timestamptz
);

CREATE UNIQUE INDEX agent_tool_calls_run_provider_call_unique
    ON agent_tool_calls (agent_run_id, parent_id, provider_call_id) NULLS NOT DISTINCT;

COMMENT ON TABLE agent_tool_calls IS 'Agent 运行中模型发起的工具调用';
COMMENT ON COLUMN agent_tool_calls.id IS '调用编号，电脑操作与审批按该编号关联';
COMMENT ON COLUMN agent_tool_calls.created_at IS '创建时间';
COMMENT ON COLUMN agent_tool_calls.updated_at IS '更新时间';
COMMENT ON COLUMN agent_tool_calls.organization_id IS '所属工作区编号';
COMMENT ON COLUMN agent_tool_calls.agent_run_id IS '所属 Agent 运行编号';
COMMENT ON COLUMN agent_tool_calls.parent_id IS '子 Agent 发起的调用所属的委派调用编号，主 Agent 的调用为空';
COMMENT ON COLUMN agent_tool_calls.model_call_id IS '发起该调用的模型调用编号';
COMMENT ON COLUMN agent_tool_calls.provider_call_id IS '模型给出的调用标识';
COMMENT ON COLUMN agent_tool_calls.name IS '工具名称，MCP 工具为服务目录中的原工具名';
COMMENT ON COLUMN agent_tool_calls.source IS '工具来源：builtin 内置、mcp MCP 服务、delegation 委派子 Agent';
COMMENT ON COLUMN agent_tool_calls.mcp_server IS 'MCP 工具所属服务名称，其他来源为空';
COMMENT ON COLUMN agent_tool_calls.arguments IS '模型给出的调用参数原文';
COMMENT ON COLUMN agent_tool_calls.replayable IS '调用中断后能否重新执行';
COMMENT ON COLUMN agent_tool_calls.side_effects IS '调用是否产生外部副作用';
COMMENT ON COLUMN agent_tool_calls.status IS '调用状态：queued 待执行、running 执行中、waiting 等待外部结果、succeeded 成功、failed 失败、cancelled 已取消、interrupted 已中断、needs_review 待核对';
COMMENT ON COLUMN agent_tool_calls.result IS '交给模型的调用结果正文';
COMMENT ON COLUMN agent_tool_calls.error IS '调用失败信息';
COMMENT ON COLUMN agent_tool_calls.evidence IS '调用结果是否构成回答依据';
COMMENT ON COLUMN agent_tool_calls.started_at IS '开始执行时间';
COMMENT ON COLUMN agent_tool_calls.completed_at IS '结束时间';

-- +goose Down
DROP TABLE agent_tool_calls;

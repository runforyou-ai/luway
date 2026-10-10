-- +goose Up
-- 创建 Agent 工具调用表。
CREATE TABLE agent_tool_calls (
    id                      uuid PRIMARY KEY,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    workspace_id            uuid NOT NULL,
    agent_run_id            uuid NOT NULL,
    parent_id               uuid,
    model_call_id           uuid NOT NULL,
    provider_call_id        text NOT NULL,
    name                    text NOT NULL,
    source                  text NOT NULL,
    mcp_server              text,
    arguments               text NOT NULL,
    replayable              boolean NOT NULL,
    side_effects            boolean NOT NULL,
    status                  text NOT NULL,
    result                  text,
    error                   text,
    evidence                boolean NOT NULL DEFAULT false,
    started_at              timestamptz,
    completed_at            timestamptz,
    computer_id             uuid,
    operation               jsonb,
    business_system_id      uuid,
    business_system_name    text,
    level                   text,
    claims                  integer NOT NULL DEFAULT 0,
    intervention            text,
    expires_at              timestamptz,
    decided_at              timestamptz,
    assignee_subject_id     uuid,
    decided_by_subject_id   uuid,
    bound_arguments         jsonb,
    local_agent_session_id  uuid,
    trace_id                text
);

CREATE INDEX agent_tool_calls_computer_queue_idx
    ON agent_tool_calls (computer_id, status, updated_at, id) WHERE ((computer_id IS NOT NULL) AND (status = ANY (ARRAY['queued'::text, 'running'::text])));

CREATE INDEX agent_tool_calls_local_agent_session_idx
    ON agent_tool_calls (local_agent_session_id, status) WHERE (local_agent_session_id IS NOT NULL);

CREATE INDEX agent_tool_calls_pending_decision_idx
    ON agent_tool_calls (workspace_id, status, created_at DESC, id DESC) WHERE (status = ANY (ARRAY['awaiting_decision'::text, 'needs_review'::text]));

CREATE UNIQUE INDEX agent_tool_calls_run_provider_call_unique
    ON agent_tool_calls (agent_run_id, parent_id, provider_call_id) NULLS NOT DISTINCT;

CREATE TRIGGER agent_tool_calls_set_updated_at BEFORE UPDATE ON agent_tool_calls FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE agent_tool_calls IS 'Agent 运行中模型发起的工具调用';
COMMENT ON COLUMN agent_tool_calls.id IS '调用编号，电脑操作与审批按该编号关联';
COMMENT ON COLUMN agent_tool_calls.created_at IS '创建时间';
COMMENT ON COLUMN agent_tool_calls.updated_at IS '更新时间';
COMMENT ON COLUMN agent_tool_calls.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN agent_tool_calls.agent_run_id IS '所属 Agent 运行编号';
COMMENT ON COLUMN agent_tool_calls.parent_id IS '子 Agent 发起的调用所属的委派调用编号，或本机 Agent 权限请求所属的委派调用编号，主 Agent 的调用为空';
COMMENT ON COLUMN agent_tool_calls.model_call_id IS '发起该调用的模型调用编号';
COMMENT ON COLUMN agent_tool_calls.provider_call_id IS '模型给出的调用标识';
COMMENT ON COLUMN agent_tool_calls.name IS '工具名称，MCP 工具为服务目录中的原工具名';
COMMENT ON COLUMN agent_tool_calls.source IS '工具来源：builtin 内置、business_system 业务系统、mcp 电脑上的本机 MCP 服务、delegation 委派子 Agent、local_agent 本机 Agent 请求权限的操作';
COMMENT ON COLUMN agent_tool_calls.mcp_server IS '本机 MCP 工具所属服务名称，其他来源为空';
COMMENT ON COLUMN agent_tool_calls.arguments IS '模型给出的调用参数原文';
COMMENT ON COLUMN agent_tool_calls.replayable IS '调用重复执行得到相同结果，中断或执行器丢失后可以重新执行';
COMMENT ON COLUMN agent_tool_calls.side_effects IS '调用是否产生外部副作用';
COMMENT ON COLUMN agent_tool_calls.status IS '调用状态：queued 待执行、running 执行中、waiting 等待外部结果或电脑领取、awaiting_decision 等待确认或审批、succeeded 成功、failed 失败、cancelled 已取消、interrupted 已中断、needs_review 待核对、rejected 已拒绝、expired 已过期、reviewed 已核对；派发到电脑的调用由电脑领取与上报推进，需要确认或审批的调用由裁决与批准后的执行推进，处理人失去处理资格或所在服务周期不再由提交它的 AI 员工处理时取消';
COMMENT ON COLUMN agent_tool_calls.result IS '交给模型的调用结果正文';
COMMENT ON COLUMN agent_tool_calls.error IS '调用失败信息';
COMMENT ON COLUMN agent_tool_calls.evidence IS '调用结果是否构成回答依据';
COMMENT ON COLUMN agent_tool_calls.started_at IS '开始执行时间';
COMMENT ON COLUMN agent_tool_calls.completed_at IS '结束时间';
COMMENT ON COLUMN agent_tool_calls.computer_id IS '执行调用的电脑编号，在服务端执行的调用为空';
COMMENT ON COLUMN agent_tool_calls.operation IS '电脑操作：派发的原语、参数与会话文件夹，以及电脑上报的文件路径、内容摘要与附带文件，仅电脑执行的调用取值';
COMMENT ON COLUMN agent_tool_calls.business_system_id IS '业务系统工具所属业务系统编号，其他来源为空';
COMMENT ON COLUMN agent_tool_calls.business_system_name IS '调用时业务系统的名称，其他来源为空';
COMMENT ON COLUMN agent_tool_calls.level IS '调用时由工具事实推导的操作级别：l0、l1、l2、l3，业务系统与电脑以外的工具为空';
COMMENT ON COLUMN agent_tool_calls.claims IS '电脑领取该调用的次数，在服务端执行的调用为 0';
COMMENT ON COLUMN agent_tool_calls.intervention IS '执行前需要的人工介入：confirmation 发起人确认（成员会话为发起成员，客服会话为已核验的客户）、approval 负责人审批，自动执行的调用为空';
COMMENT ON COLUMN agent_tool_calls.expires_at IS '确认或审批的截止时间';
COMMENT ON COLUMN agent_tool_calls.decided_at IS '确认、审批、过期、取消或核对的时间';
COMMENT ON COLUMN agent_tool_calls.assignee_subject_id IS '处理确认或审批的聊天主体：确认为发起成员或客服会话的客户，审批为 AI 员工的负责人';
COMMENT ON COLUMN agent_tool_calls.decided_by_subject_id IS '作出确认、审批或核对的聊天主体，过期或取消时为空';
COMMENT ON COLUMN agent_tool_calls.bound_arguments IS '服务端按可信上下文填入的参数，执行时覆盖同名的模型参数；没有绑定参数时为空';
COMMENT ON COLUMN agent_tool_calls.local_agent_session_id IS '委派给本机 Agent 的一轮或其权限请求所属的本机 Agent 会话编号，非空时调用由会话推进，不随运行结束';
COMMENT ON COLUMN agent_tool_calls.trace_id IS '派发到电脑时日志作用域中的串联编号，执行器执行该操作时沿用；未派发到电脑的调用为空';

-- +goose Down
DROP TABLE agent_tool_calls;

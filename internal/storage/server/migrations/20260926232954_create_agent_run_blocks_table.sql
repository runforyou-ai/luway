-- +goose Up
-- 创建已完成 Agent 运行的有序中间内容表。
CREATE TABLE agent_run_blocks (
    id               uuid PRIMARY KEY,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    organization_id  uuid NOT NULL,
    agent_run_id     uuid NOT NULL,
    "position"       bigint NOT NULL,
    model_call_id    uuid NOT NULL,
    kind             text NOT NULL,
    payload          jsonb NOT NULL
);

CREATE UNIQUE INDEX agent_run_blocks_run_position_unique
    ON agent_run_blocks (agent_run_id, "position");

COMMENT ON TABLE agent_run_blocks IS '已完成 Agent 运行的有序中间内容';
COMMENT ON COLUMN agent_run_blocks.id IS '运行时生成的内容块编号';
COMMENT ON COLUMN agent_run_blocks.created_at IS '创建时间';
COMMENT ON COLUMN agent_run_blocks.updated_at IS '更新时间';
COMMENT ON COLUMN agent_run_blocks.organization_id IS '所属工作区编号';
COMMENT ON COLUMN agent_run_blocks.agent_run_id IS '所属 Agent 运行编号';
COMMENT ON COLUMN agent_run_blocks.model_call_id IS '所属模型调用编号';
COMMENT ON COLUMN agent_run_blocks.kind IS '内容类型：thinking、content、tool_call';
COMMENT ON COLUMN agent_run_blocks.payload IS '完整文本或工具调用参数、结果和状态';

-- +goose Down
DROP TABLE agent_run_blocks;

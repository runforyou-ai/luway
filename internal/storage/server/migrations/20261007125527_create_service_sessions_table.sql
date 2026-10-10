-- +goose Up
-- 创建客户会话客服处理周期表。
CREATE TABLE service_sessions (
    id                             uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at                     timestamptz NOT NULL DEFAULT now(),
    updated_at                     timestamptz NOT NULL DEFAULT now(),
    workspace_id                   uuid NOT NULL,
    conversation_id                uuid NOT NULL,
    sequence                       bigint NOT NULL,
    status                         text NOT NULL DEFAULT 'open',
    team_id                        uuid,
    assignee_identity_id           uuid,
    opening_message_id             uuid NOT NULL,
    last_message_id                uuid NOT NULL,
    last_message_at                timestamptz NOT NULL,
    assigned_at                    timestamptz,
    first_response_at              timestamptz,
    status_changed_at              timestamptz NOT NULL DEFAULT now(),
    closed_at                      timestamptz,
    closed_by_identity_id          uuid,
    assignee_assigned_at           timestamptz,
    awaiting_reply_since           timestamptz,
    reminded_at                    timestamptz,
    close_reason                   text,
    resolution_requested_at        timestamptz,
    queued_at                      timestamptz,
    category_id                    uuid,
    rating_resolved                boolean,
    rating_comment                 text,
    rated_at                       timestamptz,
    visitor_context                jsonb,
    summary_status                 text,
    summary                        text,
    resolved                       boolean,
    summary_edited_by_identity_id  uuid,
    summary_edited_at              timestamptz,
    handoff_message_id             uuid,
    handoff_summary                jsonb,
    service_conversation_id        uuid NOT NULL,
    agent_identity_id              uuid,
    human_requested_at             timestamptz,
    human_assigned_at              timestamptz,
    human_first_response_at        timestamptz,
    human_first_response_seconds   integer
);

CREATE INDEX service_sessions_closed_idx
    ON service_sessions (workspace_id, closed_at DESC) WHERE (status = 'closed'::text);

CREATE UNIQUE INDEX service_sessions_conversation_sequence_unique
    ON service_sessions (workspace_id, service_conversation_id, sequence);

CREATE INDEX service_sessions_open_assignee_idx
    ON service_sessions (workspace_id, assignee_identity_id) WHERE (status = 'open'::text);

CREATE INDEX service_sessions_reception_idx
    ON service_sessions (workspace_id, team_id, human_requested_at) WHERE (human_first_response_seconds IS NOT NULL);

CREATE UNIQUE INDEX service_sessions_workspace_opening_message_unique
    ON service_sessions (workspace_id, opening_message_id);

CREATE UNIQUE INDEX service_sessions_workspace_service_conversation_open_unique
    ON service_sessions (workspace_id, service_conversation_id) WHERE (status = 'open'::text);

CREATE TRIGGER service_sessions_set_updated_at BEFORE UPDATE ON service_sessions FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE service_sessions IS '服务周期';
COMMENT ON COLUMN service_sessions.id IS '服务周期编号';
COMMENT ON COLUMN service_sessions.created_at IS '创建时间';
COMMENT ON COLUMN service_sessions.updated_at IS '更新时间';
COMMENT ON COLUMN service_sessions.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN service_sessions.conversation_id IS '承载服务会话的会话编号';
COMMENT ON COLUMN service_sessions.sequence IS '服务会话内的周期序号';
COMMENT ON COLUMN service_sessions.status IS '客服处理状态：open、closed';
COMMENT ON COLUMN service_sessions.team_id IS '负责团队编号';
COMMENT ON COLUMN service_sessions.assignee_identity_id IS '负责人工作区身份编号';
COMMENT ON COLUMN service_sessions.opening_message_id IS '处理周期首条消息编号';
COMMENT ON COLUMN service_sessions.last_message_id IS '处理周期最后消息编号';
COMMENT ON COLUMN service_sessions.last_message_at IS '处理周期最后消息发生时间';
COMMENT ON COLUMN service_sessions.assigned_at IS '首次分配时间';
COMMENT ON COLUMN service_sessions.first_response_at IS '首次客服响应时间';
COMMENT ON COLUMN service_sessions.status_changed_at IS '处理状态最后变更时间';
COMMENT ON COLUMN service_sessions.closed_at IS '关闭时间';
COMMENT ON COLUMN service_sessions.closed_by_identity_id IS '关闭人工作区身份编号';
COMMENT ON COLUMN service_sessions.assignee_assigned_at IS '当前负责人获得本周期的时间，周期在队列中时为空';
COMMENT ON COLUMN service_sessions.awaiting_reply_since IS '客户开始等待回复的时间，为空表示没有待回复的客户消息';
COMMENT ON COLUMN service_sessions.reminded_at IS '本轮等待已发出超时提醒的时间；负责人或等待起点变化时清空，队列中的周期换队列时同样清空';
COMMENT ON COLUMN service_sessions.close_reason IS '结束方式：ai_resolved AI 解决、customer_unresponsive 客户失联、manual 人工关闭；未关闭时为空';
COMMENT ON COLUMN service_sessions.resolution_requested_at IS 'AI 负责人请求客户确认问题是否解决的时间，包括超时跟进；周期出现新的对客消息时清空，早于当前负责人接手时间时不计入超时关单';
COMMENT ON COLUMN service_sessions.queued_at IS '周期进入当前队列的时间，开放且无负责人时有值，有负责人或已关闭时为空';
COMMENT ON COLUMN service_sessions.category_id IS '咨询分类编号，由 AI 转人工或周期小结写入，客服修改小结时可调整';
COMMENT ON COLUMN service_sessions.rating_resolved IS '访客评价的是否解决，周期关闭后由访客提交；未评价时为空';
COMMENT ON COLUMN service_sessions.rating_comment IS '访客评价的评语；未填写时为空字符串，未评价时为空';
COMMENT ON COLUMN service_sessions.rated_at IS '访客提交评价的时间；未评价时为空';
COMMENT ON COLUMN service_sessions.visitor_context IS '网站访客上下文：来源页、当前页、设备、语言、时区与地区，随该周期的访客消息更新；非网站渠道为空';
COMMENT ON COLUMN service_sessions.summary_status IS '小结状态：pending 等待生成、ready 已生成、no_request 无实质诉求、failed 生成失败；周期未关闭或不生成小结时为空';
COMMENT ON COLUMN service_sessions.summary IS '小结正文，由小结模型生成或客服填写；未生成时为空';
COMMENT ON COLUMN service_sessions.resolved IS '小结标注的是否解决，由判断模型标注或客服填写；无法判断时为空';
COMMENT ON COLUMN service_sessions.summary_edited_by_identity_id IS '最后修改小结的成员工作区身份编号；有值时小结、咨询分类与是否解决保持客服填写的结果';
COMMENT ON COLUMN service_sessions.summary_edited_at IS '客服最后修改小结的时间';
COMMENT ON COLUMN service_sessions.handoff_message_id IS '最近一次转人工系统事件的消息编号，交接摘要归属于该事件';
COMMENT ON COLUMN service_sessions.handoff_summary IS '最近一次转人工的交接摘要：request 客户诉求、progress AI 已完成的处理、blocker 需要人工处理的卡点；未生成时为空';
COMMENT ON COLUMN service_sessions.service_conversation_id IS '所属服务会话编号';
COMMENT ON COLUMN service_sessions.agent_identity_id IS '接待该周期的 AI 员工工作区身份编号：开启时或之后首次负责该周期的 AI 员工，从未由 AI 员工负责时为空';
COMMENT ON COLUMN service_sessions.human_requested_at IS '周期首次需要真人的时间：真人或队列首接待时为周期开启时间，其余为首次转人工、退回队列或交给真人负责的时间；从未需要真人时为空';
COMMENT ON COLUMN service_sessions.human_assigned_at IS '真人首次负责该周期的时间；从未由真人负责时为空';
COMMENT ON COLUMN service_sessions.human_first_response_at IS '真人首次对客回复的时间；没有真人对客回复时为空';
COMMENT ON COLUMN service_sessions.human_first_response_seconds IS '真人首响用时（秒）：首次需要真人到真人首次对客回复之间落在客服工作时间内的时长，按回复时的工作时间设置计算；没有真人对客回复或其间没有经过工作时间时为空';
COMMENT ON INDEX service_sessions_conversation_sequence_unique IS '工作区服务会话周期序号唯一索引';
COMMENT ON INDEX service_sessions_workspace_opening_message_unique IS '工作区客服处理周期首条消息唯一索引';
COMMENT ON INDEX service_sessions_workspace_service_conversation_open_unique IS '工作区服务会话未结束周期唯一索引';

-- +goose Down
DROP TABLE service_sessions;

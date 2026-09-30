-- +goose Up
-- 创建待补知识表，每个触发事件最多一条，每个客服处理周期同时最多一条待处理。
CREATE TABLE knowledge_gaps (
    id                       uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    organization_id          uuid NOT NULL,
    service_session_id       uuid NOT NULL,
    conversation_id          uuid NOT NULL,
    source                   text NOT NULL,
    trigger_message_id       uuid NOT NULL,
    occurred_at              timestamptz NOT NULL,
    question_message_id      uuid,
    status                   text NOT NULL DEFAULT 'pending',
    draft_status             text NOT NULL DEFAULT 'pending',
    draft_requested_at       timestamptz NOT NULL DEFAULT now(),
    draft_question           text,
    draft_similar_questions  jsonb,
    draft_answer             text,
    knowledge_base_id        uuid,
    qa_entry_id              uuid,
    handled_by_identity_id   uuid,
    handled_at               timestamptz
);

CREATE UNIQUE INDEX knowledge_gaps_pending_service_session_unique
    ON knowledge_gaps (organization_id, service_session_id) WHERE (status = 'pending'::text);

CREATE UNIQUE INDEX knowledge_gaps_trigger_message_unique
    ON knowledge_gaps (organization_id, trigger_message_id);

COMMENT ON TABLE knowledge_gaps IS '待补知识：AI 客服缺少知识或可能答错的客服处理周期，由管理员整理为知识库问答';
COMMENT ON COLUMN knowledge_gaps.id IS '待补知识编号';
COMMENT ON COLUMN knowledge_gaps.created_at IS '创建时间';
COMMENT ON COLUMN knowledge_gaps.updated_at IS '更新时间';
COMMENT ON COLUMN knowledge_gaps.organization_id IS '所属工作区编号';
COMMENT ON COLUMN knowledge_gaps.service_session_id IS '来源客服处理周期编号';
COMMENT ON COLUMN knowledge_gaps.conversation_id IS '来源客户会话编号';
COMMENT ON COLUMN knowledge_gaps.source IS '来源：knowledge_gap 知识不足转人工、insufficient_evidence 缺少依据转人工、rated_unresolved 访客评价未解决、possibly_wrong 判断模型标记可能答错';
COMMENT ON COLUMN knowledge_gaps.trigger_message_id IS '触发登记的系统事件消息编号：转人工、访客评价或周期关闭事件，同一事件只登记一次';
COMMENT ON COLUMN knowledge_gaps.occurred_at IS '来源发生时间：转人工、周期关闭或访客评价的时间';
COMMENT ON COLUMN knowledge_gaps.question_message_id IS '客户提问消息编号：转人工来源取转人工前客户最后一条文本，复核来源先取周期内第一条客户文本并在起草时按 AI 判断更新；客户没有文本提问时为空';
COMMENT ON COLUMN knowledge_gaps.status IS '处理状态：pending 待处理、accepted 已加入知识库、dismissed 已忽略';
COMMENT ON COLUMN knowledge_gaps.draft_status IS '问答草稿状态：pending 正在起草、ready 已起草、failed 起草失败、unavailable 未设置小结模型';
COMMENT ON COLUMN knowledge_gaps.draft_requested_at IS '最近一次请求起草的时间，起草任务只写入与之一致的请求结果';
COMMENT ON COLUMN knowledge_gaps.draft_question IS 'AI 起草的问题；未生成草稿时为空';
COMMENT ON COLUMN knowledge_gaps.draft_similar_questions IS 'AI 起草的相似问题数组；未生成草稿时为空';
COMMENT ON COLUMN knowledge_gaps.draft_answer IS 'AI 按真人客服答复起草的答案；没有真人答复时为空字符串，未生成草稿时为空';
COMMENT ON COLUMN knowledge_gaps.knowledge_base_id IS '加入的知识库编号';
COMMENT ON COLUMN knowledge_gaps.qa_entry_id IS '加入或更新的问答条目编号';
COMMENT ON COLUMN knowledge_gaps.handled_by_identity_id IS '处理该条目的成员工作区身份编号';
COMMENT ON COLUMN knowledge_gaps.handled_at IS '加入知识库或忽略的时间';

-- +goose Down
DROP TABLE knowledge_gaps;

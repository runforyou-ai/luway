-- +goose Up
-- 创建按服务对象独立的服务策略表。
CREATE TABLE service_policies (
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    workspace_id               uuid NOT NULL,
    audience                   text NOT NULL,
    business_hours_enabled     boolean NOT NULL DEFAULT false,
    business_hours_time_zone   text NOT NULL DEFAULT 'Asia/Shanghai',
    business_hours_weekly      jsonb NOT NULL DEFAULT '[[{"end": "18:00", "start": "09:00"}], [{"end": "18:00", "start": "09:00"}], [{"end": "18:00", "start": "09:00"}], [{"end": "18:00", "start": "09:00"}], [{"end": "18:00", "start": "09:00"}], [], []]'::jsonb,
    business_hours_overrides   jsonb NOT NULL DEFAULT '[]'::jsonb,
    response_reminder_minutes  integer NOT NULL DEFAULT 5,
    response_reclaim_minutes   integer NOT NULL DEFAULT 15,
    queue_reminder_minutes     integer NOT NULL DEFAULT 5,
    ai_follow_up_minutes       integer NOT NULL DEFAULT 10,
    ai_close_minutes           integer NOT NULL DEFAULT 30,
    summary_locale             text NOT NULL DEFAULT 'zh-CN',
    decision_model_id          uuid,
    summary_model_id           uuid,
    PRIMARY KEY (workspace_id, audience)
);

CREATE TRIGGER service_policies_set_updated_at BEFORE UPDATE ON service_policies FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE service_policies IS '工作区服务策略，按客户和员工分别配置';
COMMENT ON COLUMN service_policies.created_at IS '创建时间';
COMMENT ON COLUMN service_policies.updated_at IS '更新时间';
COMMENT ON COLUMN service_policies.workspace_id IS '所属工作区编号';
COMMENT ON COLUMN service_policies.business_hours_enabled IS '是否启用工作时间，未启用时始终按工作时间处理';
COMMENT ON COLUMN service_policies.business_hours_time_zone IS '工作时间使用的 IANA 时区';
COMMENT ON COLUMN service_policies.business_hours_weekly IS '周一至周日各自的工作时段列表，每段为 HH:mm 起止';
COMMENT ON COLUMN service_policies.business_hours_overrides IS '按日期覆盖的工作时段，时段为空表示当天休息';
COMMENT ON COLUMN service_policies.response_reminder_minutes IS '负责人超过该分钟数未回复发起人时提醒负责人';
COMMENT ON COLUMN service_policies.response_reclaim_minutes IS '负责人超过该分钟数未回复发起人时退回队列并重新分配，大于未响应提醒时长';
COMMENT ON COLUMN service_policies.queue_reminder_minutes IS '周期在队列中等待超过该分钟数时提醒对应处理人';
COMMENT ON COLUMN service_policies.ai_follow_up_minutes IS 'AI 负责的周期中发起人超过该分钟数未回复 AI 时，AI 跟进一次并请发起人确认问题是否解决';
COMMENT ON COLUMN service_policies.ai_close_minutes IS 'AI 跟进或请求确认后发起人超过该分钟数仍未回复时关闭周期，结束方式记为发起人未回复';
COMMENT ON COLUMN service_policies.summary_locale IS '小结与交接摘要使用的语言';
COMMENT ON COLUMN service_policies.decision_model_id IS '标注周期实质诉求、咨询分类与是否解决的判断模型编号；为空时不标注';
COMMENT ON COLUMN service_policies.summary_model_id IS '生成周期小结与交接摘要的对话模型编号；为空时不生成正文';

COMMENT ON COLUMN service_policies.audience IS '服务对象，customer 为客户，employee 为员工';

-- +goose Down
DROP TABLE service_policies;

-- +goose Up
-- 创建工作区客服设置表。
CREATE TABLE customer_service_settings (
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now(),
    organization_id            uuid PRIMARY KEY,
    business_hours_enabled     boolean NOT NULL DEFAULT false,
    business_hours_time_zone   text NOT NULL DEFAULT 'Asia/Shanghai',
    business_hours_weekly      jsonb NOT NULL DEFAULT '[[{"end": "18:00", "start": "09:00"}], [{"end": "18:00", "start": "09:00"}], [{"end": "18:00", "start": "09:00"}], [{"end": "18:00", "start": "09:00"}], [{"end": "18:00", "start": "09:00"}], [], []]'::jsonb,
    business_hours_overrides   jsonb NOT NULL DEFAULT '[]'::jsonb,
    response_reminder_minutes  integer NOT NULL DEFAULT 5,
    response_reclaim_minutes   integer NOT NULL DEFAULT 15,
    queue_reminder_minutes     integer NOT NULL DEFAULT 5,
    ai_follow_up_minutes       integer NOT NULL DEFAULT 10,
    ai_close_minutes           integer NOT NULL DEFAULT 30,
    customer_identity_secret   text,
    summary_locale             text NOT NULL DEFAULT 'zh-CN',
    decision_model_id          uuid,
    summary_model_id           uuid,
    translation_model_id       uuid
);

COMMENT ON TABLE customer_service_settings IS '工作区客服设置，每个工作区一行，创建工作区时写入';
COMMENT ON COLUMN customer_service_settings.created_at IS '创建时间';
COMMENT ON COLUMN customer_service_settings.updated_at IS '更新时间';
COMMENT ON COLUMN customer_service_settings.organization_id IS '所属工作区编号';
COMMENT ON COLUMN customer_service_settings.business_hours_enabled IS '是否启用工作时间，未启用时始终按工作时间处理';
COMMENT ON COLUMN customer_service_settings.business_hours_time_zone IS '工作时间使用的 IANA 时区';
COMMENT ON COLUMN customer_service_settings.business_hours_weekly IS '周一至周日各自的工作时段列表，每段为 HH:mm 起止';
COMMENT ON COLUMN customer_service_settings.business_hours_overrides IS '按日期覆盖的工作时段，时段为空表示当天休息';
COMMENT ON COLUMN customer_service_settings.response_reminder_minutes IS '负责人超过该分钟数未回复客户时提醒负责人';
COMMENT ON COLUMN customer_service_settings.response_reclaim_minutes IS '负责人超过该分钟数未回复客户时退回队列并重新分配，大于未响应提醒时长';
COMMENT ON COLUMN customer_service_settings.queue_reminder_minutes IS '周期在队列中等待超过该分钟数时提醒对应客服';
COMMENT ON COLUMN customer_service_settings.ai_follow_up_minutes IS 'AI 负责的周期中客户超过该分钟数未回复 AI 时，AI 跟进一次并请客户确认问题是否解决';
COMMENT ON COLUMN customer_service_settings.ai_close_minutes IS 'AI 跟进或请求确认后客户超过该分钟数仍未回复时关闭周期，结束方式记为客户失联';
COMMENT ON COLUMN customer_service_settings.customer_identity_secret IS '网站登录用户签名身份的 HMAC 密钥，base64url 字符串；未生成时为空，此时不接受签名身份';
COMMENT ON COLUMN customer_service_settings.summary_locale IS '小结与交接摘要使用的语言';
COMMENT ON COLUMN customer_service_settings.decision_model_id IS '标注周期实质诉求、咨询分类与是否解决的判断模型编号；为空时不标注';
COMMENT ON COLUMN customer_service_settings.summary_model_id IS '生成周期小结与交接摘要的对话模型编号；为空时不生成正文';
COMMENT ON COLUMN customer_service_settings.translation_model_id IS '翻译客户会话消息的对话模型编号；为空时不提供翻译';

-- +goose Down
DROP TABLE customer_service_settings;

-- +goose Up
-- 创建工作区按日运营指标表。
CREATE TABLE workspace_daily_stats (
    organization_id       uuid NOT NULL,
    stat_date             date NOT NULL,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    member_count          integer NOT NULL,
    ai_employee_count     integer NOT NULL,
    channel_count         integer NOT NULL,
    device_count          integer NOT NULL,
    storage_bytes         bigint NOT NULL,
    active_account_count  integer NOT NULL,
    message_count         integer NOT NULL,
    PRIMARY KEY (organization_id, stat_date)
);

COMMENT ON TABLE workspace_daily_stats IS '工作区按日运营指标，由运营数据汇总任务写入；规模字段是当天最后一次汇总时的取值，活跃字段统计当天全天';
COMMENT ON COLUMN workspace_daily_stats.organization_id IS '工作区编号';
COMMENT ON COLUMN workspace_daily_stats.stat_date IS '按部署统计时区划分的日期';
COMMENT ON COLUMN workspace_daily_stats.created_at IS '创建时间';
COMMENT ON COLUMN workspace_daily_stats.updated_at IS '更新时间';
COMMENT ON COLUMN workspace_daily_stats.member_count IS '有效成员数';
COMMENT ON COLUMN workspace_daily_stats.ai_employee_count IS '有效 AI 员工数';
COMMENT ON COLUMN workspace_daily_stats.channel_count IS '已启用渠道数';
COMMENT ON COLUMN workspace_daily_stats.device_count IS '未撤销电脑数';
COMMENT ON COLUMN workspace_daily_stats.storage_bytes IS '已保存文件的总字节数';
COMMENT ON COLUMN workspace_daily_stats.active_account_count IS '当天活跃账号数';
COMMENT ON COLUMN workspace_daily_stats.message_count IS '当天成员、AI 员工与客户发送的消息数，不含系统事件';

-- +goose Down
DROP TABLE workspace_daily_stats;

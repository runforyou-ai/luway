-- +goose Up
-- 创建工作区按日运营指标表。
CREATE TABLE workspace_daily_stats (
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    stat_date             date NOT NULL,
    workspace_id          uuid NOT NULL,
    member_count          integer NOT NULL,
    ai_employee_count     integer NOT NULL,
    channel_count         integer NOT NULL,
    computer_count        integer NOT NULL,
    storage_bytes         bigint NOT NULL,
    active_account_count  integer NOT NULL,
    message_count         integer NOT NULL,
    PRIMARY KEY (stat_date, workspace_id)
);

CREATE INDEX workspace_daily_stats_workspace_idx
    ON workspace_daily_stats (workspace_id, stat_date DESC);

CREATE TRIGGER workspace_daily_stats_set_updated_at BEFORE UPDATE ON workspace_daily_stats FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE workspace_daily_stats IS '工作区按日运营指标，由运营数据汇总任务写入；规模字段是当天最后一次汇总时的取值，修改统计时区全部重建时历史日期取重建时的当前值；活跃字段统计当天全天';
COMMENT ON COLUMN workspace_daily_stats.created_at IS '创建时间';
COMMENT ON COLUMN workspace_daily_stats.updated_at IS '更新时间';
COMMENT ON COLUMN workspace_daily_stats.stat_date IS '按平台统计时区划分的日期';
COMMENT ON COLUMN workspace_daily_stats.workspace_id IS '工作区编号';
COMMENT ON COLUMN workspace_daily_stats.member_count IS '有效成员数';
COMMENT ON COLUMN workspace_daily_stats.ai_employee_count IS '有效 AI 员工数';
COMMENT ON COLUMN workspace_daily_stats.channel_count IS '已启用渠道数';
COMMENT ON COLUMN workspace_daily_stats.computer_count IS '未撤销电脑数';
COMMENT ON COLUMN workspace_daily_stats.storage_bytes IS '已保存文件的总字节数';
COMMENT ON COLUMN workspace_daily_stats.active_account_count IS '当天活跃账号数';
COMMENT ON COLUMN workspace_daily_stats.message_count IS '当天成员、AI 员工与客户发送的消息数，不含系统事件';

-- +goose Down
DROP TABLE workspace_daily_stats;

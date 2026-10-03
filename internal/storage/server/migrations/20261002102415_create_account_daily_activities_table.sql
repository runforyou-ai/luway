-- +goose Up
-- 创建账号按日活跃明细表。
CREATE TABLE account_daily_activities (
    organization_id  uuid NOT NULL,
    activity_date    date NOT NULL,
    account_id       uuid NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, activity_date, account_id)
);

COMMENT ON TABLE account_daily_activities IS '账号按日活跃明细：账号当天在工作区发送消息或操作客服处理周期时有一行，由运营数据汇总任务从消息记录推导';
COMMENT ON COLUMN account_daily_activities.organization_id IS '工作区编号';
COMMENT ON COLUMN account_daily_activities.activity_date IS '按部署统计时区划分的日期';
COMMENT ON COLUMN account_daily_activities.account_id IS '账号编号';
COMMENT ON COLUMN account_daily_activities.created_at IS '创建时间';

-- +goose Down
DROP TABLE account_daily_activities;

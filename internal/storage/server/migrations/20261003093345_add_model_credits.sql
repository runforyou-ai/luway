-- +goose Up
-- 平台时区改为运营统计与每日赠送积分共用，增加每日赠送积分、平台模型价格和调用扣除积分。
ALTER TABLE platforms RENAME COLUMN statistics_time_zone TO time_zone;
ALTER TABLE platforms ADD COLUMN daily_credit_grant bigint NOT NULL DEFAULT 0;

ALTER TABLE ai_models
    ADD COLUMN input_credit_price bigint,
    ADD COLUMN output_credit_price bigint,
    ADD COLUMN request_credit_price bigint;

ALTER TABLE ai_model_calls
    ADD COLUMN input_credit_price bigint,
    ADD COLUMN output_credit_price bigint,
    ADD COLUMN request_credit_price bigint,
    ADD COLUMN credits bigint NOT NULL DEFAULT 0,
    ADD COLUMN credit_shortfall bigint NOT NULL DEFAULT 0;

COMMENT ON COLUMN platforms.time_zone IS '平台时区（IANA），运营数据按日统计与每日赠送积分按此划分日期';
COMMENT ON COLUMN platforms.statistics_rebuild_pending IS '修改平台时区后等待按新时区全部重建运营数据';
COMMENT ON COLUMN platforms.daily_credit_grant IS '每个工作区每天赠送的积分，当天结束时未用完的部分作废，0 表示不赠送';
COMMENT ON COLUMN ai_models.input_credit_price IS '平台模型每百万输入 Token 的积分价格，三项价格同时为空表示未定价，工作区模型为空';
COMMENT ON COLUMN ai_models.output_credit_price IS '平台模型每百万输出 Token 的积分价格';
COMMENT ON COLUMN ai_models.request_credit_price IS '平台模型每次调用的积分价格';
COMMENT ON COLUMN ai_model_calls.input_credit_price IS '调用时平台模型每百万输入 Token 的积分价格，工作区模型为空';
COMMENT ON COLUMN ai_model_calls.output_credit_price IS '调用时平台模型每百万输出 Token 的积分价格，工作区模型为空';
COMMENT ON COLUMN ai_model_calls.request_credit_price IS '调用时平台模型每次调用的积分价格，工作区模型为空';
COMMENT ON COLUMN ai_model_calls.credits IS '从工作区余额扣除的积分：进行中为预占积分，结束后为实际扣除积分';
COMMENT ON COLUMN ai_model_calls.credit_shortfall IS '实际费用超出预占且余额不足补扣的积分';

-- +goose Down
ALTER TABLE ai_model_calls
    DROP COLUMN credit_shortfall,
    DROP COLUMN credits,
    DROP COLUMN request_credit_price,
    DROP COLUMN output_credit_price,
    DROP COLUMN input_credit_price;

ALTER TABLE ai_models
    DROP COLUMN request_credit_price,
    DROP COLUMN output_credit_price,
    DROP COLUMN input_credit_price;

ALTER TABLE platforms DROP COLUMN daily_credit_grant;
ALTER TABLE platforms RENAME COLUMN time_zone TO statistics_time_zone;
COMMENT ON COLUMN platforms.statistics_time_zone IS '运营数据按日统计使用的 IANA 时区';
COMMENT ON COLUMN platforms.statistics_rebuild_pending IS '修改统计时区后等待按新时区全部重建运营数据';

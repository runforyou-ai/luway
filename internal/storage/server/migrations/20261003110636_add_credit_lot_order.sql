-- +goose Up
-- 积分批次增加商业服务充值订单来源。
ALTER TABLE credit_lots ADD COLUMN order_id text;

CREATE UNIQUE INDEX credit_lots_order_id_unique ON credit_lots (order_id);

COMMENT ON COLUMN credit_lots.source IS '入账来源：daily_grant 每日赠送、adjustment 平台管理员调整、purchase 商业服务充值';
COMMENT ON COLUMN credit_lots.order_id IS '来源为商业服务充值时的积分订单编号，每个订单最多一条';
COMMENT ON COLUMN credit_lots.expires_at IS '过期时间，为空表示不过期；充值订单退款时为退款时间；过期后剩余积分作废';
COMMENT ON COLUMN credit_movements.source_type IS '变动来源类型：model_call 模型调用、adjustment 平台管理员调整、refund 充值订单退款';
COMMENT ON COLUMN credit_movements.source_id IS '变动来源编号：模型调用编号、调整编号或退款的批次编号';

-- +goose Down
COMMENT ON COLUMN credit_movements.source_id IS '变动来源编号：模型调用编号或调整编号';
COMMENT ON COLUMN credit_movements.source_type IS '变动来源类型：model_call 模型调用、adjustment 平台管理员调整';
COMMENT ON COLUMN credit_lots.expires_at IS '过期时间，为空表示不过期；过期后剩余积分作废';
COMMENT ON COLUMN credit_lots.source IS '入账来源：daily_grant 每日赠送、adjustment 平台管理员调整';
DROP INDEX credit_lots_order_id_unique;
ALTER TABLE credit_lots DROP COLUMN order_id;

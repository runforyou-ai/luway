-- +goose Up
-- 创建积分批次表。
CREATE TABLE credit_lots (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at       timestamptz NOT NULL DEFAULT now(),
    organization_id  uuid NOT NULL,
    source           text NOT NULL,
    adjustment_id    uuid,
    grant_date       date,
    amount           bigint NOT NULL,
    remaining        bigint NOT NULL,
    expires_at       timestamptz
);

CREATE UNIQUE INDEX credit_lots_organization_grant_date_unique
    ON credit_lots (organization_id, grant_date);

COMMENT ON TABLE credit_lots IS '积分批次，每次入账一条；工作区余额为未过期批次的剩余积分之和';
COMMENT ON COLUMN credit_lots.id IS '批次编号';
COMMENT ON COLUMN credit_lots.created_at IS '入账时间';
COMMENT ON COLUMN credit_lots.organization_id IS '所属工作区编号';
COMMENT ON COLUMN credit_lots.source IS '入账来源：daily_grant 每日赠送、adjustment 平台管理员调整';
COMMENT ON COLUMN credit_lots.adjustment_id IS '来源为平台管理员调整时的调整编号';
COMMENT ON COLUMN credit_lots.grant_date IS '每日赠送所属的平台时区日期，每个工作区每天最多一条';
COMMENT ON COLUMN credit_lots.amount IS '入账积分';
COMMENT ON COLUMN credit_lots.remaining IS '剩余积分，等于入账积分加上该批次全部变动';
COMMENT ON COLUMN credit_lots.expires_at IS '过期时间，为空表示不过期；过期后剩余积分作废';

-- +goose Down
DROP TABLE credit_lots;

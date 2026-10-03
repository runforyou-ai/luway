-- +goose Up
-- 创建积分调整表。
CREATE TABLE credit_adjustments (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at       timestamptz NOT NULL DEFAULT now(),
    organization_id  uuid NOT NULL,
    account_id       uuid NOT NULL,
    amount           bigint NOT NULL,
    note             text NOT NULL
);

COMMENT ON TABLE credit_adjustments IS '平台管理员对工作区积分的手动调整';
COMMENT ON COLUMN credit_adjustments.id IS '调整编号';
COMMENT ON COLUMN credit_adjustments.created_at IS '调整时间';
COMMENT ON COLUMN credit_adjustments.organization_id IS '被调整的工作区编号';
COMMENT ON COLUMN credit_adjustments.account_id IS '执行调整的平台管理员账号编号';
COMMENT ON COLUMN credit_adjustments.amount IS '实际变动积分，正数为增加，负数为扣减；扣减最多扣到余额为 0';
COMMENT ON COLUMN credit_adjustments.note IS '调整备注';

-- +goose Down
DROP TABLE credit_adjustments;

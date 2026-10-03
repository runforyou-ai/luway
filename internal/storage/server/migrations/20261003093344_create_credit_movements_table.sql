-- +goose Up
-- 创建积分批次变动表。
CREATE TABLE credit_movements (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    created_at       timestamptz NOT NULL DEFAULT now(),
    organization_id  uuid NOT NULL,
    lot_id           uuid NOT NULL,
    amount           bigint NOT NULL,
    source_type      text NOT NULL,
    source_id        uuid NOT NULL
);

COMMENT ON TABLE credit_movements IS '积分批次变动，只追加不修改';
COMMENT ON COLUMN credit_movements.id IS '变动编号';
COMMENT ON COLUMN credit_movements.created_at IS '变动时间';
COMMENT ON COLUMN credit_movements.organization_id IS '所属工作区编号';
COMMENT ON COLUMN credit_movements.lot_id IS '变动的批次编号';
COMMENT ON COLUMN credit_movements.amount IS '变动积分，负数为从批次扣减，正数为退回批次';
COMMENT ON COLUMN credit_movements.source_type IS '变动来源类型：model_call 模型调用、adjustment 平台管理员调整';
COMMENT ON COLUMN credit_movements.source_id IS '变动来源编号：模型调用编号或调整编号';

-- +goose Down
DROP TABLE credit_movements;

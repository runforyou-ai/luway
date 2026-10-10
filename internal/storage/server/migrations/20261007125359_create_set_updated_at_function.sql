-- +goose Up
-- 创建在更新行时写入 updated_at 的触发器函数，各表的 BEFORE UPDATE 触发器调用它。
-- +goose StatementBegin
CREATE FUNCTION set_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION set_updated_at() IS '更新行时把 updated_at 写为事务时刻';

-- +goose Down
DROP FUNCTION set_updated_at();

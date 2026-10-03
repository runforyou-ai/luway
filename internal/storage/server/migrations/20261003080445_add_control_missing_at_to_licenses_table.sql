-- +goose Up
-- 授权表记录 control 中查不到本服务器授权的起始时间。
ALTER TABLE licenses ADD COLUMN control_missing_at timestamptz;

COMMENT ON COLUMN licenses.control_missing_at IS '与 control 同步时查不到本服务器授权的起始时间，同步取得授权码或替换授权码时清空';

-- +goose Down
ALTER TABLE licenses DROP COLUMN control_missing_at;

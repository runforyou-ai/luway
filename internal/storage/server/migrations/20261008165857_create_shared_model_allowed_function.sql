-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION shared_model_allowed(workspace_id uuid, model_id uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT false
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION shared_model_allowed(uuid, uuid) IS '判断共享模型对工作区的可用性，默认关闭共享模型';

-- +goose Down
DROP FUNCTION shared_model_allowed(uuid, uuid);

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION auto_pool_member_allowed(workspace_id uuid, member_id uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT false
$$;
-- +goose StatementEnd

COMMENT ON FUNCTION auto_pool_member_allowed(uuid, uuid) IS '判断 Auto 池成员对工作区是否可用，默认关闭；托管迁移可覆盖为按定价与境外权限开放';

-- +goose Down
DROP FUNCTION auto_pool_member_allowed(uuid, uuid);

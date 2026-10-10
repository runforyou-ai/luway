-- +goose Up
-- 创建公众号授权表。
CREATE TABLE wechat_authorizations (
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    channel_id        uuid PRIMARY KEY,
    workspace_id      uuid NOT NULL,
    component_app_id  text NOT NULL,
    refresh_token     text NOT NULL,
    permission_ids    integer[] NOT NULL,
    nick_name         text NOT NULL,
    head_image_url    text NOT NULL DEFAULT '',
    principal_name    text NOT NULL DEFAULT '',
    user_name         text NOT NULL DEFAULT '',
    authorized_at     timestamptz NOT NULL,
    revoked_at        timestamptz
);

CREATE TRIGGER wechat_authorizations_set_updated_at BEFORE UPDATE ON wechat_authorizations FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE wechat_authorizations IS '授权接入公众号渠道获得的公众号授权与公众号资料，公众号 AppID 为渠道的 provider_account_id';
COMMENT ON COLUMN wechat_authorizations.created_at IS '创建时间';
COMMENT ON COLUMN wechat_authorizations.updated_at IS '更新时间';
COMMENT ON COLUMN wechat_authorizations.channel_id IS '授权接入公众号渠道';
COMMENT ON COLUMN wechat_authorizations.workspace_id IS '渠道所属企业';
COMMENT ON COLUMN wechat_authorizations.component_app_id IS '获得授权的第三方平台 Component AppID，与部署当前平台不一致时须重新授权';
COMMENT ON COLUMN wechat_authorizations.refresh_token IS '授权方刷新令牌，用于获取公众号接口调用凭据';
COMMENT ON COLUMN wechat_authorizations.permission_ids IS '公众号授予的权限集编号';
COMMENT ON COLUMN wechat_authorizations.nick_name IS '公众号名称';
COMMENT ON COLUMN wechat_authorizations.head_image_url IS '公众号头像地址';
COMMENT ON COLUMN wechat_authorizations.principal_name IS '公众号主体名称';
COMMENT ON COLUMN wechat_authorizations.user_name IS '公众号原始 ID';
COMMENT ON COLUMN wechat_authorizations.authorized_at IS '最近一次授权或更新授权的时间，按微信事件时间判定先后';
COMMENT ON COLUMN wechat_authorizations.revoked_at IS '公众号取消授权的时间，之后重新授权时清空';

-- +goose Down
DROP TABLE wechat_authorizations;

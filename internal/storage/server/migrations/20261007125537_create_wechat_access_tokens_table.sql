-- +goose Up
-- 创建微信接口调用凭据表。
CREATE TABLE wechat_access_tokens (
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    app_id              text NOT NULL,
    access_token        text,
    expires_at          timestamptz,
    failed_at           timestamptz,
    failure             text NOT NULL DEFAULT '',
    failure_detail      text NOT NULL DEFAULT '',
    refresh_started_at  timestamptz,
    credential          text NOT NULL,
    PRIMARY KEY (credential, app_id)
);

CREATE TRIGGER wechat_access_tokens_set_updated_at BEFORE UPDATE ON wechat_access_tokens FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE wechat_access_tokens IS '微信接口调用凭据，按凭据类别与 AppID 一行；同一时刻只有一台服务器持有刷新租约并请求微信，各服务器读取同一凭据';
COMMENT ON COLUMN wechat_access_tokens.created_at IS '创建时间';
COMMENT ON COLUMN wechat_access_tokens.updated_at IS '更新时间';
COMMENT ON COLUMN wechat_access_tokens.app_id IS '凭据所属的第三方平台 Component AppID 或公众号 AppID';
COMMENT ON COLUMN wechat_access_tokens.access_token IS '接口调用凭据，尚未成功获取时为空';
COMMENT ON COLUMN wechat_access_tokens.expires_at IS '凭据到期时间';
COMMENT ON COLUMN wechat_access_tokens.failed_at IS '最近一次获取失败的时间，成功后清空';
COMMENT ON COLUMN wechat_access_tokens.failure IS '最近一次获取失败的原因：ip_not_whitelisted 出口 IP 未加入白名单、rejected 微信拒绝、unavailable 无法连接；成功后为空';
COMMENT ON COLUMN wechat_access_tokens.failure_detail IS '失败详情：白名单失败时为微信识别的调用方 IP，微信拒绝时为错误码与说明，无法连接时为错误信息';
COMMENT ON COLUMN wechat_access_tokens.refresh_started_at IS '当前刷新租约的开始时间，刷新结束后清空；超过租约时限视为刷新中断';
COMMENT ON COLUMN wechat_access_tokens.credential IS '凭据类别：platform 第三方平台凭据、key 密钥接入公众号凭据、authorization 授权接入公众号凭据';

-- +goose Down
DROP TABLE wechat_access_tokens;

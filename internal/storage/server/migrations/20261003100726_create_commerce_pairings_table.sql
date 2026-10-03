-- +goose Up
-- 创建商业服务配对表。
CREATE TABLE commerce_pairings (
    server_id        uuid PRIMARY KEY,
    created_at       timestamptz NOT NULL DEFAULT now(),
    url              text NOT NULL,
    service_id       text NOT NULL,
    public_key       bytea NOT NULL,
    change_sequence  bigint NOT NULL DEFAULT 0,
    synced_at        timestamptz,
    failed_at        timestamptz,
    failure          text NOT NULL DEFAULT ''
);

COMMENT ON TABLE commerce_pairings IS '平台与商业服务的配对，每个服务器标识至多一行；重置服务器标识或解除配对时删除';
COMMENT ON COLUMN commerce_pairings.server_id IS '完成配对的服务器标识';
COMMENT ON COLUMN commerce_pairings.created_at IS '配对时间';
COMMENT ON COLUMN commerce_pairings.url IS '商业服务地址';
COMMENT ON COLUMN commerce_pairings.service_id IS '商业服务标识，即商业服务签名的 keyid';
COMMENT ON COLUMN commerce_pairings.public_key IS '商业服务的 Ed25519 签名公钥';
COMMENT ON COLUMN commerce_pairings.change_sequence IS '已应用的商业服务变更源最后一个序号';
COMMENT ON COLUMN commerce_pairings.synced_at IS '最近一次成功读取变更源的时间';
COMMENT ON COLUMN commerce_pairings.failed_at IS '最近一次读取变更源失败的时间，成功后清空';
COMMENT ON COLUMN commerce_pairings.failure IS '最近一次读取变更源失败的原因：unavailable 无法连接、not_paired 商业服务中未配对、invalid_data 数据不符合约定、failed 其他错误；成功后为空';

-- +goose Down
DROP TABLE commerce_pairings;

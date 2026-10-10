-- +goose Up
-- 创建 ACME HTTP-01 质询表。
CREATE TABLE acme_challenges (
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    token              text PRIMARY KEY,
    key_authorization  text NOT NULL
);

CREATE TRIGGER acme_challenges_set_updated_at BEFORE UPDATE ON acme_challenges FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON TABLE acme_challenges IS '签发证书期间等待 ACME 服务验证的 HTTP-01 质询，任一直接提供 HTTPS 的服务器都按令牌应答';
COMMENT ON COLUMN acme_challenges.created_at IS '创建时间';
COMMENT ON COLUMN acme_challenges.updated_at IS '更新时间';
COMMENT ON COLUMN acme_challenges.token IS 'HTTP-01 质询令牌';
COMMENT ON COLUMN acme_challenges.key_authorization IS '应答质询的密钥授权';

-- +goose Down
DROP TABLE acme_challenges;

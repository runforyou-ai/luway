//go:build server

package models

import (
	"time"

	"github.com/uptrace/bun"
)

// ACMEChallenge 表示签发证书期间等待 ACME 服务验证的一项 HTTP-01 质询。
type ACMEChallenge struct {
	bun.BaseModel `bun:"table:acme_challenges,alias:ach"`

	Token            string    `bun:"token,pk"`
	KeyAuthorization string    `bun:"key_authorization"`
	CreatedAt        time.Time `bun:"created_at,nullzero,default:now()"`
	UpdatedAt        time.Time `bun:"updated_at,nullzero,default:now()"`
}

//go:build server

package computer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/random"
	"github.com/uptrace/bun"
)

// AuthenticateComputerQuery 以电脑凭据认证电脑。
type AuthenticateComputerQuery struct {
	db *bun.DB
}

// NewAuthenticateComputerQuery 创建电脑认证查询。
func NewAuthenticateComputerQuery(db *bun.DB) *AuthenticateComputerQuery {
	return &AuthenticateComputerQuery{db: db}
}

// Execute 返回凭据对应的未撤销电脑；工作区已暂停，或个人电脑主人的账号或成员身份已停用时返回 ErrCredentialInvalid。
func (q *AuthenticateComputerQuery) Execute(ctx context.Context, credential string) (Identity, error) {
	if credential == "" {
		return Identity{}, ErrCredentialInvalid
	}
	var identity Identity
	err := q.db.NewRaw(`
		SELECT cmp.workspace_id::text, cmp.id::text
		FROM computers AS cmp
		JOIN workspaces AS o ON o.id = cmp.workspace_id
		LEFT JOIN users AS u ON u.id = cmp.owner_user_id AND u.workspace_id = cmp.workspace_id
		LEFT JOIN accounts AS acc ON acc.id = u.account_id
		WHERE cmp.credential_hash = ?
		  AND cmp.revoked_at IS NULL
		  AND o.lifecycle_status = ?
		  AND (cmp.kind = ? OR (u.status = ? AND acc.status = ?))
	`, random.HashToken(credential), domain.WorkspaceLifecycleActive, domain.ComputerKindWorkspace, domain.IdentityStatusActive, domain.AccountStatusActive).
		Scan(ctx, &identity.WorkspaceID, &identity.ComputerID)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, ErrCredentialInvalid
	}
	if err != nil {
		return Identity{}, fmt.Errorf("authenticate computer: %w", err)
	}
	return identity, nil
}

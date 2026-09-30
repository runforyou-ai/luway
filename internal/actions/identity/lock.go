//go:build server

// Package identity 提供 Action 层共用的身份查询和当前用户写入守卫。
package identity

import (
	"context"
	"errors"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// ErrInvalid 表示当前用户与企业关联已经失效。
var ErrInvalid = errors.New("identity association is invalid")

// LockActiveUser 校验当前身份并锁定有效用户账号，供写事务复用。
func LockActiveUser(ctx context.Context, tx bun.Tx, identity *servermodels.Identity) error {
	return LockActiveUserAccounts(ctx, tx, identity, nil)
}

// LockActiveUserAccounts 按账号编号锁定操作者及关联账号，并校验操作者身份与有效状态。
func LockActiveUserAccounts(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, relatedUserIDs []string) error {
	if identity == nil ||
		!common.ValidUUID(identity.Organization.ID) ||
		!common.ValidUUID(identity.OrganizationIdentity.ID) ||
		!common.ValidUUID(identity.User.ID) ||
		!common.ValidUUID(identity.User.IdentityID) ||
		identity.OrganizationIdentity.ID != identity.User.IdentityID ||
		identity.OrganizationIdentity.OrganizationID != identity.Organization.ID ||
		identity.OrganizationIdentity.Type != string(domain.OrganizationIdentityTypeUser) ||
		identity.User.OrganizationID != identity.Organization.ID {
		return ErrInvalid
	}
	userIDs := append([]string{identity.User.ID}, relatedUserIDs...)
	var users []servermodels.User
	err := tx.NewSelect().
		Model(&users).
		Column("id", "identity_id", "status").
		Where("id IN (?)", bun.In(userIDs)).
		Where("organization_id = ?", identity.Organization.ID).
		OrderExpr("id ASC").
		For("NO KEY UPDATE").
		Scan(ctx)
	if err != nil {
		return err
	}
	for _, user := range users {
		if user.ID == identity.User.ID && user.IdentityID == identity.User.IdentityID && user.Status == string(domain.IdentityStatusActive) {
			return nil
		}
	}
	return ErrInvalid
}

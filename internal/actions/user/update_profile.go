//go:build server

package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	fileaction "github.com/runforyou-ai/cervi/internal/actions/file"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// UpdateProfileAction 修改当前成员的个人资料。
type UpdateProfileAction struct {
	db *bun.DB
}

// NewUpdateProfileAction 创建个人资料修改操作。
func NewUpdateProfileAction(db *bun.DB) *UpdateProfileAction {
	return &UpdateProfileAction{db: db}
}

// Execute 校验并更新当前成员的姓名和头像关联，以及所属账号的名称和邮箱。
func (a *UpdateProfileAction) Execute(ctx context.Context, identity *servermodels.Identity, input ProfileInput) (*servermodels.Identity, error) {
	input, fields := normalizeProfileInput(input)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	var updatedIdentity *servermodels.Identity
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		var previousAvatarFileID, nextAvatarFileID *string
		identityQuery := tx.NewUpdate().
			Model((*servermodels.OrganizationIdentity)(nil)).
			Set("display_name = ?", input.DisplayName).
			Set("updated_at = now()")
		if input.AvatarFileID != "" {
			currentIdentity := &servermodels.OrganizationIdentity{}
			if err := tx.NewSelect().Model(currentIdentity).
				Column("avatar_file_id").
				Where("oi.id = ?", identity.User.IdentityID).
				Where("oi.organization_id = ?", identity.Organization.ID).
				Where("oi.type = ?", domain.OrganizationIdentityTypeUser).
				For("UPDATE").
				Scan(ctx); errors.Is(err, sql.ErrNoRows) {
				return identityaction.ErrInvalid
			} else if err != nil {
				return err
			}
			previousAvatarFileID = currentIdentity.AvatarFileID
			var err error
			nextAvatarFileID, err = fileaction.ActivateLinkedImage(ctx, tx, identity.Organization.ID, domain.FilePurposeUserAvatar, input.AvatarFileID, previousAvatarFileID)
			if err != nil {
				return err
			}
			identityQuery = identityQuery.Set("avatar_file_id = ?", *nextAvatarFileID)
		}
		// 最近使用的姓名同步为账号名称，新建或加入其他工作区时以它作为默认成员姓名。
		if _, err := tx.NewUpdate().Model((*servermodels.Account)(nil)).
			Set("display_name = ?", input.DisplayName).
			Set("updated_at = now()").
			Where("id = ?", identity.Account.ID).
			Where("display_name IS DISTINCT FROM ?", input.DisplayName).
			Exec(ctx); err != nil {
			return err
		}
		err := identityaction.UpdateAccountEmail(ctx, tx, identity.Account.ID, input.Email)
		if errors.Is(err, identityaction.ErrAccountEmailTaken) {
			return &ValidationError{Fields: map[string]ValidationCode{"email": ValidationEmailDuplicate}}
		}
		if err != nil {
			return err
		}
		displayChanged, err := identityaction.UpdateUserIdentity(ctx, tx, identity.Organization.ID, identity.User.IdentityID, identityQuery)
		if err != nil {
			return err
		}
		if err := fileaction.RetireLinkedImage(ctx, tx, identity.Organization.ID, previousAvatarFileID, nextAvatarFileID); err != nil {
			return err
		}
		// 名称或头像实际变化时，在资料与头像文件写入完成后推进展示本人的会话版本。
		if displayChanged {
			if err := chatstate.TouchIdentityConversations(ctx, tx, identity.Organization.ID, identity.User.IdentityID); err != nil {
				return err
			}
		}
		updatedIdentity, err = loadCurrentIdentity(ctx, tx, identity)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("update profile: %w", err)
	}
	return updatedIdentity, nil
}

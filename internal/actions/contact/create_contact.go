//go:build server

package contact

import (
	"context"
	"fmt"

	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// CreateContactAction 创建外部联系人。
type CreateContactAction struct {
	db *bun.DB
}

// NewCreateContactAction 创建联系人新增操作。
func NewCreateContactAction(db *bun.DB) *CreateContactAction {
	return &CreateContactAction{db: db}
}

// Execute 校验字段并在当前企业中创建联系人。
func (a *CreateContactAction) Execute(ctx context.Context, identity *servermodels.Identity, input ContactInput) (*ContactDetail, error) {
	input, fields := normalizeNewContactInput(input)
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	var detail *ContactDetail
	err := a.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := identityaction.LockActiveUser(ctx, tx, identity); err != nil {
			return err
		}
		if err := validateSourceChannel(ctx, tx, identity.Organization.ID, input.ChannelID); err != nil {
			return err
		}
		number, err := allocateContactNumber(ctx, tx, identity.Organization.ID)
		if err != nil {
			return err
		}
		contact := &servermodels.Contact{
			OrganizationID:  identity.Organization.ID,
			Number:          number,
			CreatedByUserID: &identity.User.ID,
			SourceChannelID: input.ChannelID,
			Stage:           string(input.Stage),
		}
		if input.DisplayName != "" {
			contact.DisplayName = &input.DisplayName
		}
		if input.Notes != "" {
			contact.Notes = &input.Notes
		}
		if _, err := tx.NewInsert().
			Model(contact).
			Column("organization_id", "number", "created_by_user_id", "source_channel_id", "display_name", "stage", "notes").
			Returning("*").
			Exec(ctx); err != nil {
			return err
		}
		if err := replaceMethods(ctx, tx, contact.OrganizationID, contact.ID, input.Methods); err != nil {
			return err
		}
		loaded, err := loadContactDetail(ctx, tx, contact.OrganizationID, contact.ID)
		if err != nil {
			return err
		}
		detail = loaded
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("create contact: %w", err)
	}
	return detail, nil
}

//go:build server

package contact

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	"github.com/runforyou-ai/cervi/internal/actions/contactprofile"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// EnsureChannelIdentityInput 定义渠道自动联系人所需的稳定标识。
type EnsureChannelIdentityInput struct {
	OrganizationID string
	ChannelID      string
	ExternalID     string
	ContactID      string
	IdentityID     string
	// ExternalUserID 是验签通过的企业用户编号，非空时渠道身份关联到该编号的联系人。
	ExternalUserID string
	// Email 是签名身份中的规范化邮箱，非空时补充为联系人的联系方式。
	Email string
}

// EnsuredChannelIdentity 返回联系人和渠道身份。
type EnsuredChannelIdentity struct {
	Contact  *servermodels.Contact
	Identity *servermodels.ContactChannelIdentity
}

// EnsureChannelIdentity 在调用方事务中取得或创建联系人渠道身份；已有联系人被恢复或补充邮箱时推进其全部客户会话的版本。
func EnsureChannelIdentity(ctx context.Context, db bun.IDB, input EnsureChannelIdentityInput) (EnsuredChannelIdentity, error) {
	identity := &servermodels.ContactChannelIdentity{}
	err := db.NewSelect().
		Model(identity).
		Where("cci.organization_id = ?", input.OrganizationID).
		Where("cci.channel_id = ?", input.ChannelID).
		Where("cci.external_id = ?", input.ExternalID).
		For("UPDATE").
		Scan(ctx)
	if err == nil {
		// 读取渠道身份所属的同企业联系人。
		contact := &servermodels.Contact{}
		if err := db.NewSelect().
			Model(contact).
			Where("ct.organization_id = ?", input.OrganizationID).
			Where("ct.id = ?", identity.ContactID).
			Scan(ctx); err != nil {
			return EnsuredChannelIdentity{}, fmt.Errorf("load channel identity contact: %w", err)
		}
		restored, err := restoreContact(ctx, db, contact)
		if err != nil {
			return EnsuredChannelIdentity{}, err
		}
		added, err := contactprofile.AddMethod(ctx, db, contact.OrganizationID, contact.ID, domain.ContactMethodTypeEmail, input.Email)
		if err != nil {
			return EnsuredChannelIdentity{}, err
		}
		if restored || added {
			if err := chatstate.TouchContactProfileConversations(ctx, db, contact.OrganizationID, contact.ID); err != nil {
				return EnsuredChannelIdentity{}, err
			}
		}
		return EnsuredChannelIdentity{Contact: contact, Identity: identity}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return EnsuredChannelIdentity{}, fmt.Errorf("find channel identity: %w", err)
	}

	contact, restored, err := ensureChannelContact(ctx, db, input)
	if err != nil {
		return EnsuredChannelIdentity{}, err
	}
	added, err := contactprofile.AddMethod(ctx, db, contact.OrganizationID, contact.ID, domain.ContactMethodTypeEmail, input.Email)
	if err != nil {
		return EnsuredChannelIdentity{}, err
	}
	if restored || added {
		if err := chatstate.TouchContactProfileConversations(ctx, db, contact.OrganizationID, contact.ID); err != nil {
			return EnsuredChannelIdentity{}, err
		}
	}
	identity = &servermodels.ContactChannelIdentity{
		ID:             input.IdentityID,
		OrganizationID: input.OrganizationID,
		ContactID:      contact.ID,
		ChannelID:      input.ChannelID,
		ExternalID:     input.ExternalID,
	}
	if _, err := db.NewInsert().
		Model(identity).
		Column("id", "organization_id", "contact_id", "channel_id", "external_id", "display_name").
		Exec(ctx); err != nil {
		return EnsuredChannelIdentity{}, fmt.Errorf("create channel identity: %w", err)
	}
	return EnsuredChannelIdentity{Contact: contact, Identity: identity}, nil
}

// ensureChannelContact 为新渠道身份取得联系人并返回是否恢复了已软删除的联系人：带企业用户编号时复用该编号的联系人（含已软删除的），否则新建自动联系人。
// 并发首次写入同一企业用户编号时由唯一约束拒绝，调用方重试后读到已提交的联系人。
func ensureChannelContact(ctx context.Context, db bun.IDB, input EnsureChannelIdentityInput) (*servermodels.Contact, bool, error) {
	if input.ExternalUserID != "" {
		contact := &servermodels.Contact{}
		err := db.NewSelect().
			Model(contact).
			Where("ct.organization_id = ?", input.OrganizationID).
			Where("ct.external_user_id = ?", input.ExternalUserID).
			For("UPDATE").
			Scan(ctx)
		if err == nil {
			restored, err := restoreContact(ctx, db, contact)
			return contact, restored, err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, false, fmt.Errorf("find contact by external user id: %w", err)
		}
	}
	number, err := allocateContactNumber(ctx, db, input.OrganizationID)
	if err != nil {
		return nil, false, err
	}
	contact := &servermodels.Contact{
		ID:              input.ContactID,
		OrganizationID:  input.OrganizationID,
		Number:          number,
		SourceChannelID: input.ChannelID,
		Stage:           string(domain.ContactStageVisitor),
	}
	if input.ExternalUserID != "" {
		contact.ExternalUserID = &input.ExternalUserID
	}
	if _, err := db.NewInsert().
		Model(contact).
		Column("id", "organization_id", "number", "created_by_user_id", "source_channel_id", "display_name", "stage", "notes", "external_user_id").
		Exec(ctx); err != nil {
		return nil, false, fmt.Errorf("create automatic contact: %w", err)
	}
	return contact, false, nil
}

// restoreContact 恢复已移入回收站的联系人，返回是否实际恢复。
func restoreContact(ctx context.Context, db bun.IDB, contact *servermodels.Contact) (bool, error) {
	if contact.DeletedAt == nil {
		return false, nil
	}
	if _, err := db.NewUpdate().
		Model(contact).
		Set("deleted_at = NULL").
		Set("updated_at = now()").
		WherePK().
		Where("organization_id = ?", contact.OrganizationID).
		Exec(ctx); err != nil {
		return false, fmt.Errorf("restore automatic contact: %w", err)
	}
	contact.DeletedAt = nil
	return true, nil
}

// allocateContactNumber 在调用方事务中递增工作区的联系人编号计数并返回新编号。
func allocateContactNumber(ctx context.Context, db bun.IDB, organizationID string) (int64, error) {
	var number int64
	if err := db.NewUpdate().TableExpr("organizations").
		Set("last_contact_number = last_contact_number + 1").
		Where("id = ?", organizationID).
		Returning("last_contact_number").
		Scan(ctx, &number); err != nil {
		return 0, fmt.Errorf("allocate contact number: %w", err)
	}
	return number, nil
}

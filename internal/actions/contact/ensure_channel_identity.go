//go:build server

package contact

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/contactprofile"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// EnsureChannelIdentityInput 定义渠道自动联系人所需的稳定标识。
type EnsureChannelIdentityInput struct {
	OrganizationID string
	ChannelID      string
	ExternalID     string
	ContactID      string
	IdentityID     string
	// VerifiedUserID 是本次签名身份验签通过的企业用户编号；渠道身份的核验状态与之同步，为空时渠道身份为未核验。
	VerifiedUserID string
	// Email 是签名身份中的规范化邮箱，非空时补充为联系人的联系方式。
	Email string
}

// EnsuredChannelIdentity 返回联系人和渠道身份。
type EnsuredChannelIdentity struct {
	Contact  *servermodels.Contact
	Identity *servermodels.ContactChannelIdentity
}

// EnsureChannelIdentity 在调用方事务中取得或创建联系人渠道身份：已有渠道身份原样返回，核验状态与联系方式由 SyncChannelIdentity 在确认写入新消息后同步；新建时按本次签名身份关联联系人、记录核验身份并补充邮箱，已有联系人被恢复或补充邮箱时推进其全部客户会话的版本。
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
	if input.VerifiedUserID != "" {
		identity.VerifiedUserID = &input.VerifiedUserID
	}
	if _, err := db.NewInsert().
		Model(identity).
		Column("id", "organization_id", "contact_id", "channel_id", "external_id", "verified_user_id", "display_name").
		Exec(ctx); err != nil {
		return EnsuredChannelIdentity{}, fmt.Errorf("create channel identity: %w", err)
	}
	return EnsuredChannelIdentity{Contact: contact, Identity: identity}, nil
}

// SyncChannelIdentity 在调用方事务中让已取得的渠道身份与本次签名身份一致：恢复回收站中的联系人，同步核验状态与所属联系人，补充签名邮箱；返回渠道身份当前所属的联系人。
// 核验身份或所属联系人变化时取消该渠道身份会话中按原身份装配的在途运行。调用方只在确认本次写入新消息后调用，重放的消息不改变身份。
func SyncChannelIdentity(ctx context.Context, db bun.IDB, input EnsureChannelIdentityInput, ensured EnsuredChannelIdentity) (EnsuredChannelIdentity, error) {
	identity := ensured.Identity
	previousUserID, previousContactID := identity.VerifiedUserID, identity.ContactID
	contact, restored, err := syncChannelIdentityVerification(ctx, db, input, identity, ensured.Contact)
	if err != nil {
		return EnsuredChannelIdentity{}, err
	}
	if identity.ContactID != previousContactID || (previousUserID == nil) != (identity.VerifiedUserID == nil) ||
		(previousUserID != nil && *previousUserID != *identity.VerifiedUserID) {
		if _, err := chatstate.CancelChannelIdentityRuns(ctx, db, identity.OrganizationID, identity.ID, domain.AgentRunErrorCodeCustomerIdentityChanged); err != nil {
			return EnsuredChannelIdentity{}, err
		}
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

// syncChannelIdentityVerification 让已有渠道身份的核验状态与本次签名身份一致，返回渠道身份当前所属的联系人及该联系人是否被恢复。
// 未带签名身份时只清除核验；签名身份的企业用户编号已有联系人时把渠道身份移到该联系人，没有时由当前联系人承接该编号，当前联系人已属于其他企业用户时新建联系人承接。
func syncChannelIdentityVerification(ctx context.Context, db bun.IDB, input EnsureChannelIdentityInput, identity *servermodels.ContactChannelIdentity, contact *servermodels.Contact) (*servermodels.Contact, bool, error) {
	current := ""
	if identity.VerifiedUserID != nil {
		current = *identity.VerifiedUserID
	}
	if current == input.VerifiedUserID {
		restored, err := restoreContact(ctx, db, contact)
		return contact, restored, err
	}
	if input.VerifiedUserID == "" {
		if err := setChannelIdentityVerifiedUser(ctx, db, identity, nil); err != nil {
			return nil, false, err
		}
		restored, err := restoreContact(ctx, db, contact)
		return contact, restored, err
	}

	target := &servermodels.Contact{}
	err := db.NewSelect().
		Model(target).
		Where("ct.organization_id = ?", input.OrganizationID).
		Where("ct.external_user_id = ?", input.VerifiedUserID).
		For("UPDATE").
		Scan(ctx)
	switch {
	case err == nil:
	case !errors.Is(err, sql.ErrNoRows):
		return nil, false, fmt.Errorf("find contact by verified user id: %w", err)
	case contact.ExternalUserID == nil:
		// 当前联系人尚未关联企业用户时直接承接该编号。
		if _, err := db.NewUpdate().
			Model(contact).
			Set("external_user_id = ?", input.VerifiedUserID).
			Set("updated_at = now()").
			WherePK().
			Where("organization_id = ?", contact.OrganizationID).
			Exec(ctx); err != nil {
			return nil, false, fmt.Errorf("bind contact external user id: %w", err)
		}
		contact.ExternalUserID = &input.VerifiedUserID
		target = contact
	default:
		target, _, err = ensureChannelContact(ctx, db, input)
		if err != nil {
			return nil, false, err
		}
	}
	restored, err := restoreContact(ctx, db, target)
	if err != nil {
		return nil, false, err
	}
	if target.ID != contact.ID {
		if err := moveChannelIdentity(ctx, db, identity, contact, target); err != nil {
			return nil, false, err
		}
	}
	if err := setChannelIdentityVerifiedUser(ctx, db, identity, &input.VerifiedUserID); err != nil {
		return nil, false, err
	}
	return target, restored, nil
}

// setChannelIdentityVerifiedUser 写入渠道身份核验的企业用户编号，空值表示未核验，并推进其客户会话的版本。
func setChannelIdentityVerifiedUser(ctx context.Context, db bun.IDB, identity *servermodels.ContactChannelIdentity, userID *string) error {
	if _, err := db.NewUpdate().
		Model(identity).
		Set("verified_user_id = ?", userID).
		Set("updated_at = now()").
		WherePK().
		Where("organization_id = ?", identity.OrganizationID).
		Exec(ctx); err != nil {
		return fmt.Errorf("update channel identity verified user: %w", err)
	}
	identity.VerifiedUserID = userID
	return chatstate.TouchChannelIdentityConversations(ctx, db, identity.OrganizationID, identity.ID)
}

// moveChannelIdentity 把渠道身份及其会话从原联系人移到目标联系人：会话中联系人的参与者、发起人与请求人改为目标联系人；原联系人未关联企业用户且不再有渠道身份时移入回收站。
func moveChannelIdentity(ctx context.Context, db bun.IDB, identity *servermodels.ContactChannelIdentity, from, to *servermodels.Contact) error {
	organizationID := identity.OrganizationID
	conversations := db.NewSelect().TableExpr("channel_conversations AS cc").
		Column("cc.conversation_id").
		Where("cc.organization_id = ? AND cc.contact_channel_identity_id = ?", organizationID, identity.ID)
	var fromSubjectID string
	err := db.NewSelect().TableExpr("chat_subjects AS cs").Column("cs.id").
		Where("cs.organization_id = ? AND cs.kind = ? AND cs.source_id = ?", organizationID, domain.ChatSubjectKindContact, from.ID).
		Scan(ctx, &fromSubjectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("find source contact chat subject: %w", err)
	}
	// 原联系人已有聊天主体时，其在这些会话中的参与记录改由目标联系人的聊天主体承接。
	if fromSubjectID != "" {
		var toSubjectID string
		err := db.NewSelect().TableExpr("chat_subjects AS cs").Column("cs.id").
			Where("cs.organization_id = ? AND cs.kind = ? AND cs.source_id = ?", organizationID, domain.ChatSubjectKindContact, to.ID).
			Scan(ctx, &toSubjectID)
		if errors.Is(err, sql.ErrNoRows) {
			subject := &servermodels.ChatSubject{OrganizationID: organizationID, Kind: string(domain.ChatSubjectKindContact), SourceID: to.ID}
			_, err = db.NewInsert().Model(subject).
				Column("organization_id", "kind", "source_id").
				Returning("id").
				Exec(ctx)
			toSubjectID = subject.ID
		}
		if err != nil {
			return fmt.Errorf("ensure target contact chat subject: %w", err)
		}
		if _, err := db.NewUpdate().TableExpr("conversation_participants").
			Set("subject_id = ?", toSubjectID).
			Set("updated_at = now()").
			Where("organization_id = ? AND subject_id = ? AND conversation_id IN (?)", organizationID, fromSubjectID, conversations).
			Exec(ctx); err != nil {
			return fmt.Errorf("move contact conversation participants: %w", err)
		}
		if _, err := db.NewUpdate().TableExpr("service_conversations").
			Set("requester_subject_id = ?", toSubjectID).
			Set("updated_at = now()").
			Where("organization_id = ? AND requester_subject_id = ? AND conversation_id IN (?)", organizationID, fromSubjectID, conversations).
			Exec(ctx); err != nil {
			return fmt.Errorf("move contact service conversation requester: %w", err)
		}
		if _, err := db.NewUpdate().TableExpr("conversations").
			Set("created_by_subject_id = ?", toSubjectID).
			Set("updated_at = now()").
			Where("organization_id = ? AND created_by_subject_id = ? AND id IN (?)", organizationID, fromSubjectID, conversations).
			Exec(ctx); err != nil {
			return fmt.Errorf("move contact conversation creator: %w", err)
		}
	}
	if _, err := db.NewUpdate().
		Model(identity).
		Set("contact_id = ?", to.ID).
		Set("updated_at = now()").
		WherePK().
		Where("organization_id = ?", organizationID).
		Exec(ctx); err != nil {
		return fmt.Errorf("move channel identity: %w", err)
	}
	identity.ContactID = to.ID
	// 未关联企业用户的原联系人不再有渠道身份时移入回收站。
	if from.ExternalUserID == nil {
		if _, err := db.NewUpdate().TableExpr("contacts AS ct").
			Set("deleted_at = now()").
			Set("updated_at = now()").
			Where("ct.organization_id = ? AND ct.id = ? AND ct.deleted_at IS NULL", organizationID, from.ID).
			Where("NOT EXISTS (SELECT 1 FROM contact_channel_identities AS other WHERE other.organization_id = ct.organization_id AND other.contact_id = ct.id)").
			Exec(ctx); err != nil {
			return fmt.Errorf("delete emptied contact: %w", err)
		}
	}
	return chatstate.TouchContactProfileConversations(ctx, db, organizationID, to.ID)
}

// ensureChannelContact 为新渠道身份取得联系人并返回是否恢复了已软删除的联系人：带企业用户编号时复用该编号的联系人（含已软删除的），否则新建自动联系人。
// 并发首次写入同一企业用户编号时由唯一约束拒绝，调用方重试后读到已提交的联系人。
func ensureChannelContact(ctx context.Context, db bun.IDB, input EnsureChannelIdentityInput) (*servermodels.Contact, bool, error) {
	if input.VerifiedUserID != "" {
		contact := &servermodels.Contact{}
		err := db.NewSelect().
			Model(contact).
			Where("ct.organization_id = ?", input.OrganizationID).
			Where("ct.external_user_id = ?", input.VerifiedUserID).
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
	if input.VerifiedUserID != "" {
		contact.ExternalUserID = &input.VerifiedUserID
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

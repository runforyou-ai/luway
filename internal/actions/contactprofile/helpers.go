//go:build server

package contactprofile

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/uptrace/bun"
)

// normalizeName 规范化并校验字段或标签名称，返回空字段码表示通过。
func normalizeName(name string, maxLength int) (string, common.FieldCode) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return name, ValidationNameRequired
	case utf8.RuneCountInString(name) > maxLength:
		return name, ValidationNameTooLong
	}
	return name, ""
}

// normalizeAIInstruction 去除 AI 填写说明或添加条件的首尾空白并校验长度，返回空字段码表示通过。
func normalizeAIInstruction(instruction string) (string, common.FieldCode) {
	instruction = strings.TrimSpace(instruction)
	if utf8.RuneCountInString(instruction) > domain.ContactProfileAIInstructionMaxLength {
		return instruction, ValidationAIInstructionTooLong
	}
	return instruction, ""
}

// lockContact 对未删除的联系人取 FOR KEY SHARE，与联系人删除互斥。
func lockContact(ctx context.Context, tx bun.Tx, organizationID, contactID string) error {
	if !common.ValidUUID(contactID) {
		return ErrContactNotFound
	}
	var id string
	err := tx.NewSelect().TableExpr("contacts AS c").Column("c.id").
		Where("c.organization_id = ? AND c.id = ? AND c.deleted_at IS NULL", organizationID, contactID).
		For("KEY SHARE").
		Scan(ctx, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrContactNotFound
	}
	return err
}

// touchContact 在档案实际变化后更新联系人的更新时间，并推进其客户会话版本以通知客户端重读档案。
func touchContact(ctx context.Context, tx bun.IDB, organizationID, contactID string) error {
	if _, err := tx.NewUpdate().TableExpr("contacts").
		Set("updated_at = now()").
		Where("organization_id = ? AND id = ?", organizationID, contactID).
		Exec(ctx); err != nil {
		return err
	}
	return chatstate.TouchContactProfileConversations(ctx, tx, organizationID, contactID)
}

// syncedFromWebsite 在编辑未影响记录时区分无变化与网站来源：query 选出的档案记录来源为网站时返回 ErrSyncedFromWebsite。
func syncedFromWebsite(ctx context.Context, query *bun.SelectQuery) error {
	synced, err := query.Where("source = ?", domain.ContactProfileSourceWebsite).Exists(ctx)
	if err != nil {
		return err
	}
	if synced {
		return ErrSyncedFromWebsite
	}
	return nil
}

// changed 判断写入语句是否影响了记录。
func changed(result sql.Result) (bool, error) {
	rows, err := result.RowsAffected()
	return rows > 0, err
}

// touchTagContacts 推进拥有指定标签的联系人的客户会话版本，通知客户端重读档案。
func touchTagContacts(ctx context.Context, tx bun.Tx, organizationID, tagID string) error {
	return chatstate.TouchContactsProfileConversations(ctx, tx, organizationID, tx.NewSelect().TableExpr("contact_tag_assignments AS cta").
		Column("cta.contact_id").
		Where("cta.organization_id = ? AND cta.tag_id = ?", organizationID, tagID))
}

// touchFieldContacts 推进在指定字段上有取值的联系人的客户会话版本，通知客户端重读档案。
func touchFieldContacts(ctx context.Context, tx bun.Tx, organizationID, fieldID string) error {
	return chatstate.TouchContactsProfileConversations(ctx, tx, organizationID, tx.NewSelect().TableExpr("contact_field_values AS cfv").
		Column("cfv.contact_id").
		Where("cfv.organization_id = ? AND cfv.field_id = ?", organizationID, fieldID))
}

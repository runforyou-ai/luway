//go:build server

package contactprofile

import (
	"context"
	"database/sql"
	"errors"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// lockContact 对未删除的联系人取 FOR KEY SHARE，与联系人删除互斥。
func lockContact(ctx context.Context, tx bun.Tx, workspaceID, contactID string) error {
	if !str.IsUUID(contactID) {
		return ErrContactNotFound
	}
	var id string
	err := tx.NewSelect().TableExpr("contacts AS c").Column("c.id").
		Where("c.workspace_id = ? AND c.id = ? AND c.deleted_at IS NULL", workspaceID, contactID).
		For("KEY SHARE").
		Scan(ctx, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrContactNotFound
	}
	return err
}

// touchContact 在档案实际变化后更新联系人的更新时间，并推进其客户会话版本以通知客户端重读档案。
func touchContact(ctx context.Context, tx bun.IDB, workspaceID, contactID string) error {
	if _, err := tx.NewUpdate().TableExpr("contacts").
		Set("updated_at = now()").
		Where("workspace_id = ? AND id = ?", workspaceID, contactID).
		Exec(ctx); err != nil {
		return err
	}
	return chatstate.TouchContactProfileConversations(ctx, tx, workspaceID, contactID)
}

// syncedFromSignedIdentity 在编辑未影响记录时区分无变化与签名身份来源：query 选出的档案记录来源为签名身份时返回 ErrSyncedFromSignedIdentity。
func syncedFromSignedIdentity(ctx context.Context, query *bun.SelectQuery) error {
	synced, err := query.Where("source = ?", domain.ContactProfileSourceSignedIdentity).Exists(ctx)
	if err != nil {
		return err
	}
	if synced {
		return ErrSyncedFromSignedIdentity
	}
	return nil
}

// changed 判断写入语句是否影响了记录。
func changed(result sql.Result) (bool, error) {
	rows, err := result.RowsAffected()
	return rows > 0, err
}

// touchTagContacts 推进拥有指定标签的联系人的客户会话版本，通知客户端重读档案。
func touchTagContacts(ctx context.Context, tx bun.Tx, workspaceID, tagID string) error {
	return chatstate.TouchContactsProfileConversations(ctx, tx, workspaceID, tx.NewSelect().TableExpr("contact_tag_assignments AS cta").
		Column("cta.contact_id").
		Where("cta.workspace_id = ? AND cta.tag_id = ?", workspaceID, tagID))
}

// touchFieldContacts 推进在指定字段上有取值的联系人的客户会话版本，通知客户端重读档案。
func touchFieldContacts(ctx context.Context, tx bun.Tx, workspaceID, fieldID string) error {
	return chatstate.TouchContactsProfileConversations(ctx, tx, workspaceID, tx.NewSelect().TableExpr("contact_field_values AS cfv").
		Column("cfv.contact_id").
		Where("cfv.workspace_id = ? AND cfv.field_id = ?", workspaceID, fieldID))
}

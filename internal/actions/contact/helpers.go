//go:build server

package contact

import (
	"context"
	"database/sql"
	"errors"

	"github.com/runforyou-ai/luway/internal/actions/contactprofile"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// validateSourceChannel 校验来源渠道属于当前企业且已启用。
func validateSourceChannel(ctx context.Context, tx bun.Tx, workspaceID, channelID string) error {
	exists, err := tx.NewSelect().
		Model((*servermodels.Channel)(nil)).
		Where("workspace_id = ?", workspaceID).
		Where("id = ?", channelID).
		Where("enabled = TRUE").
		Exists(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return &ValidationError{Fields: map[string]ValidationCode{"channelId": ValidationChannelInvalid}}
	}
	return nil
}

// replaceMethods 同步联系人联系方式并保留未变更记录，同时刷新联系人的首选邮箱并返回其是否变化。
func replaceMethods(ctx context.Context, tx bun.Tx, workspaceID, contactID string, methods []MethodInput) (bool, error) {
	existing := make([]servermodels.ContactMethod, 0)
	if err := tx.NewSelect().
		Model(&existing).
		Where("cm.workspace_id = ?", workspaceID).
		Where("cm.contact_id = ?", contactID).
		For("UPDATE").
		Scan(ctx); err != nil {
		return false, err
	}

	type methodKey struct {
		typeName string
		value    string
	}
	desired := arr.KeyBy(methods, func(method MethodInput) methodKey {
		return methodKey{typeName: string(method.Type), value: method.Value}
	})

	existingByKey := make(map[methodKey]*servermodels.ContactMethod, len(existing))
	obsoleteIDs := make([]string, 0)
	for index := range existing {
		record := &existing[index]
		recordKey := methodKey{typeName: record.Type, value: record.NormalizedValue}
		if _, wanted := desired[recordKey]; wanted {
			existingByKey[recordKey] = record
			continue
		}
		obsoleteIDs = append(obsoleteIDs, record.ID)
	}
	if len(obsoleteIDs) > 0 {
		if _, err := tx.NewDelete().
			Model((*servermodels.ContactMethod)(nil)).
			Where("workspace_id = ?", workspaceID).
			Where("contact_id = ?", contactID).
			Where("id IN (?)", bun.List(obsoleteIDs)).
			Exec(ctx); err != nil {
			return false, err
		}
	}

	// 切换主要联系方式前取消原主要项。
	for recordKey, record := range existingByKey {
		if record.IsPrimary && !desired[recordKey].IsPrimary {
			if _, err := tx.NewUpdate().
				Model((*servermodels.ContactMethod)(nil)).
				Set("is_primary = false").
				Where("workspace_id = ?", workspaceID).
				Where("contact_id = ?", contactID).
				Where("id = ?", record.ID).
				Exec(ctx); err != nil {
				return false, err
			}
			record.IsPrimary = false
		}
	}

	newRecords := make([]servermodels.ContactMethod, 0)
	for _, method := range methods {
		record := existingByKey[methodKey{typeName: string(method.Type), value: method.Value}]
		if record == nil {
			newRecord := servermodels.ContactMethod{
				WorkspaceID:     workspaceID,
				ContactID:       contactID,
				Type:            string(method.Type),
				Value:           method.Value,
				NormalizedValue: method.Value,
				IsPrimary:       method.IsPrimary,
				Label:           support.NilIfZero(method.Label),
			}
			newRecords = append(newRecords, newRecord)
			continue
		}

		labelMatches := (record.Label == nil && method.Label == "") ||
			(record.Label != nil && *record.Label == method.Label)
		if labelMatches && record.IsPrimary == method.IsPrimary {
			continue
		}
		if _, err := tx.NewUpdate().
			Model((*servermodels.ContactMethod)(nil)).
			Set("label = ?", support.NilIfZero(method.Label)).
			Set("is_primary = ?", method.IsPrimary).
			Where("workspace_id = ?", workspaceID).
			Where("contact_id = ?", contactID).
			Where("id = ?", record.ID).
			Exec(ctx); err != nil {
			return false, err
		}
	}
	if len(newRecords) > 0 {
		if _, err := tx.NewInsert().
			Model(&newRecords).
			Column("workspace_id", "contact_id", "type", "value", "normalized_value", "label", "is_primary").
			Exec(ctx); err != nil {
			return false, err
		}
	}
	return contactprofile.RefreshPrimaryEmail(ctx, tx, workspaceID, contactID)
}

// contactAvatarFileIDColumn 读取联系人头像：取最近更新且带头像的渠道身份头像，联系人表别名为 c。
const contactAvatarFileIDColumn = "(SELECT ci.avatar_file_id::text FROM channel_identities AS ci WHERE ci.workspace_id = c.workspace_id AND ci.contact_id = c.id AND ci.avatar_file_id IS NOT NULL ORDER BY ci.updated_at DESC, ci.id DESC LIMIT 1) AS avatar_file_id"

// loadContact 读取当前企业中未删除的联系人。
func loadContact(ctx context.Context, db bun.IDB, workspaceID, contactID string) (*ContactRecord, error) {
	contact := &ContactRecord{}
	query := db.NewSelect().
		TableExpr("contacts AS c").
		ColumnExpr("c.id::text AS id").
		Column("number", "source_channel_id", "display_name", "stage", "notes", "created_at").
		Where("c.id = ?", contactID).
		Where("c.workspace_id = ?", workspaceID).
		Where("c.deleted_at IS NULL")
	if err := query.Scan(ctx, contact); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	return contact, nil
}

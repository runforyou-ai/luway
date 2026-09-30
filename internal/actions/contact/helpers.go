//go:build server

package contact

import (
	"context"
	"database/sql"
	"errors"

	"github.com/runforyou-ai/luway/internal/common"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// validateSourceChannel 校验来源渠道属于当前企业且已启用。
func validateSourceChannel(ctx context.Context, tx bun.Tx, organizationID, channelID string) error {
	exists, err := tx.NewSelect().
		Model((*servermodels.Channel)(nil)).
		Where("organization_id = ?", organizationID).
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

// replaceMethods 同步联系人联系方式并保留未变更记录。
func replaceMethods(ctx context.Context, tx bun.Tx, organizationID, contactID string, methods []MethodInput) error {
	existing := make([]servermodels.ContactMethod, 0)
	if err := tx.NewSelect().
		Model(&existing).
		Where("cm.organization_id = ?", organizationID).
		Where("cm.contact_id = ?", contactID).
		For("UPDATE").
		Scan(ctx); err != nil {
		return err
	}

	type methodKey struct {
		typeName string
		value    string
	}
	desired := make(map[methodKey]MethodInput, len(methods))
	for _, method := range methods {
		desired[methodKey{typeName: string(method.Type), value: method.Value}] = method
	}

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
			Where("organization_id = ?", organizationID).
			Where("contact_id = ?", contactID).
			Where("id IN (?)", bun.In(obsoleteIDs)).
			Exec(ctx); err != nil {
			return err
		}
	}

	// 切换主要联系方式前取消原主要项。
	for recordKey, record := range existingByKey {
		if record.IsPrimary && !desired[recordKey].IsPrimary {
			if _, err := tx.NewUpdate().
				Model((*servermodels.ContactMethod)(nil)).
				Set("is_primary = false").
				Set("updated_at = now()").
				Where("organization_id = ?", organizationID).
				Where("contact_id = ?", contactID).
				Where("id = ?", record.ID).
				Exec(ctx); err != nil {
				return err
			}
			record.IsPrimary = false
		}
	}

	newRecords := make([]servermodels.ContactMethod, 0)
	for _, method := range methods {
		record := existingByKey[methodKey{typeName: string(method.Type), value: method.Value}]
		if record == nil {
			newRecord := servermodels.ContactMethod{
				OrganizationID:  organizationID,
				ContactID:       contactID,
				Type:            string(method.Type),
				Value:           method.Value,
				NormalizedValue: method.Value,
				IsPrimary:       method.IsPrimary,
			}
			if method.Label != "" {
				newRecord.Label = &method.Label
			}
			newRecords = append(newRecords, newRecord)
			continue
		}

		labelMatches := (record.Label == nil && method.Label == "") ||
			(record.Label != nil && *record.Label == method.Label)
		if labelMatches && record.IsPrimary == method.IsPrimary {
			continue
		}
		var label *string
		if method.Label != "" {
			label = &method.Label
		}
		if _, err := tx.NewUpdate().
			Model((*servermodels.ContactMethod)(nil)).
			Set("label = ?", label).
			Set("is_primary = ?", method.IsPrimary).
			Set("updated_at = now()").
			Where("organization_id = ?", organizationID).
			Where("contact_id = ?", contactID).
			Where("id = ?", record.ID).
			Exec(ctx); err != nil {
			return err
		}
	}
	if len(newRecords) == 0 {
		return nil
	}
	_, err := tx.NewInsert().
		Model(&newRecords).
		Column("organization_id", "contact_id", "type", "value", "normalized_value", "label", "is_primary").
		Exec(ctx)
	return err
}

// contactAvatarFileIDColumn 读取联系人头像：取最近更新且带头像的渠道身份头像，联系人表别名为 c。
const contactAvatarFileIDColumn = "(SELECT cci.avatar_file_id::text FROM contact_channel_identities AS cci WHERE cci.organization_id = c.organization_id AND cci.contact_id = c.id AND cci.avatar_file_id IS NOT NULL ORDER BY cci.updated_at DESC, cci.id DESC LIMIT 1) AS avatar_file_id"

// loadContact 读取当前企业中未删除的联系人。
func loadContact(ctx context.Context, db bun.IDB, organizationID, contactID string) (*ContactRecord, error) {
	if !common.ValidUUID(contactID) {
		return nil, ErrNotFound
	}
	contact := &ContactRecord{}
	query := db.NewSelect().
		TableExpr("contacts AS c").
		ColumnExpr("c.id::text AS id").
		Column("number", "source_channel_id", "display_name", "stage", "notes", "created_at").
		Where("c.id = ?", contactID).
		Where("c.organization_id = ?", organizationID).
		Where("c.deleted_at IS NULL")
	if err := query.Scan(ctx, contact); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	return contact, nil
}

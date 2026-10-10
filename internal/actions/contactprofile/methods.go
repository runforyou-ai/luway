//go:build server

package contactprofile

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// AddMethod 为联系人追加一条联系方式并返回是否新增：该类型没有主要联系方式时设为主要，已有相同取值或数量达到上限时不写入。
func AddMethod(ctx context.Context, db bun.IDB, workspaceID, contactID string, methodType domain.ContactMethodType, value string) (bool, error) {
	if value == "" {
		return false, nil
	}
	result, err := db.NewRaw(`INSERT INTO contact_methods (workspace_id, contact_id, type, value, normalized_value, is_primary)
		SELECT ?, ?, ?, ?, ?, NOT EXISTS (
			SELECT 1 FROM contact_methods WHERE workspace_id = ? AND contact_id = ? AND type = ? AND is_primary
		)
		WHERE (SELECT count(*) FROM contact_methods WHERE workspace_id = ? AND contact_id = ?) < ?
		ON CONFLICT DO NOTHING`,
		workspaceID, contactID, methodType, value, value, workspaceID, contactID, methodType,
		workspaceID, contactID, domain.ContactMethodsMaxCount,
	).Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("add contact method: %w", err)
	}
	added, err := changed(result)
	if err != nil || !added || methodType != domain.ContactMethodTypeEmail {
		return added, err
	}
	if _, err := RefreshPrimaryEmail(ctx, db, workspaceID, contactID); err != nil {
		return false, err
	}
	return true, nil
}

// RefreshPrimaryEmail 按当前联系方式重写联系人的首选邮箱并返回是否变化；contact_methods 的每条写入路径都须在同一事务中调用。
func RefreshPrimaryEmail(ctx context.Context, db bun.IDB, workspaceID, contactID string) (bool, error) {
	result, err := db.NewRaw(`UPDATE contacts AS c SET primary_email = preferred.value
		FROM (SELECT (SELECT cm.value FROM contact_methods AS cm
			WHERE cm.workspace_id = ? AND cm.contact_id = ? AND cm.type = ?
			ORDER BY cm.is_primary DESC, cm.created_at ASC, cm.id ASC LIMIT 1) AS value) AS preferred
		WHERE c.workspace_id = ? AND c.id = ? AND c.primary_email IS DISTINCT FROM preferred.value`,
		workspaceID, contactID, domain.ContactMethodTypeEmail, workspaceID, contactID,
	).Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("refresh contact primary email: %w", err)
	}
	return changed(result)
}

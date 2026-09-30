//go:build server

package contactprofile

import (
	"context"
	"fmt"

	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/uptrace/bun"
)

// AddMethod 为联系人追加一条联系方式并返回是否新增：该类型没有主要联系方式时设为主要，已有相同取值或数量达到上限时不写入。
func AddMethod(ctx context.Context, db bun.IDB, organizationID, contactID string, methodType domain.ContactMethodType, value string) (bool, error) {
	if value == "" {
		return false, nil
	}
	result, err := db.NewRaw(`INSERT INTO contact_methods (organization_id, contact_id, type, value, normalized_value, is_primary)
		SELECT ?, ?, ?, ?, ?, NOT EXISTS (
			SELECT 1 FROM contact_methods WHERE organization_id = ? AND contact_id = ? AND type = ? AND is_primary
		)
		WHERE (SELECT count(*) FROM contact_methods WHERE organization_id = ? AND contact_id = ?) < ?
		ON CONFLICT DO NOTHING`,
		organizationID, contactID, methodType, value, value, organizationID, contactID, methodType,
		organizationID, contactID, domain.ContactMethodsMaxCount,
	).Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("add contact method: %w", err)
	}
	return changed(result)
}

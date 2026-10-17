//go:build server

package contact

import (
	"context"

	"github.com/runforyou-ai/luway/internal/actions/contactprofile"
	"github.com/uptrace/bun"
)

// RefreshResolvedDisplayName 按档案名称、最近渠道身份名称、首选邮箱重写联系人的成员界面名称，并返回是否变化。
func RefreshResolvedDisplayName(ctx context.Context, db bun.IDB, workspaceID, contactID string) (bool, error) {
	return contactprofile.RefreshResolvedDisplayName(ctx, db, workspaceID, contactID)
}

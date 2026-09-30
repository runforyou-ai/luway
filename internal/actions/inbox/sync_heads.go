//go:build server

package inbox

import (
	"context"

	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

// SyncHeads 定义当前用户可见会话集合、身份资料与个人置顶顺序的探针值。
type SyncHeads struct {
	ConversationCount      int    `bun:"conversation_count"`
	ConversationChecksum   string `bun:"conversation_checksum"`
	IdentityProfileVersion int64  `bun:"identity_profile_version"`
	PinOrderVersion        int64  `bun:"pin_order_version"`
}

// SyncHeads 在一条语句的快照内聚合可见会话版本校验和、身份资料版本与个人置顶顺序版本。
func (q *LoadInboxQuery) SyncHeads(ctx context.Context, identity *servermodels.Identity) (SyncHeads, error) {
	organizationID, identityID := identity.Organization.ID, identity.OrganizationIdentity.ID
	// 服务会话覆盖全部视图与服务状态，内部会话沿用列表资格。
	visible := q.serviceConversationAccessQuery(organizationID, identityID).Where("msg.id IS NOT NULL").
		UnionAll(q.directConversationsQuery(organizationID, identityID)).
		UnionAll(q.agentConversationsQuery(organizationID, identityID)).
		UnionAll(q.groupConversationAccessQuery(organizationID, identityID))
	profileVersion := q.db.NewSelect().Table("users").Column("profile_version").
		Where("organization_id = ? AND id = ?", organizationID, identity.User.ID)
	pinOrderVersion := q.db.NewSelect().Table("users").Column("pin_order_version").
		Where("organization_id = ? AND id = ?", organizationID, identity.User.ID)
	var heads SyncHeads
	err := q.db.NewSelect().TableExpr("(?) AS visible", visible).
		Join("JOIN conversations AS cv ON cv.organization_id = ? AND cv.id = visible.id", organizationID).
		Join("LEFT JOIN conversation_user_states AS cus ON cus.organization_id = cv.organization_id AND cus.conversation_id = cv.id AND cus.user_id = ?", identity.User.ID).
		ColumnExpr("count(*) AS conversation_count").
		// 每项 64 位哈希平移到无符号区间后求和，再按 2^64 取模。
		ColumnExpr("(COALESCE(sum(hashtextextended(concat_ws(':', cv.id, cv.version, COALESCE(cus.version, 0)), 0)::numeric + 9223372036854775808), 0) % 18446744073709551616)::text AS conversation_checksum").
		ColumnExpr("(?) AS identity_profile_version", profileVersion).
		ColumnExpr("(?) AS pin_order_version", pinOrderVersion).
		Scan(ctx, &heads)
	return heads, err
}

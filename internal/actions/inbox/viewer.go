//go:build server

package inbox

import (
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// Viewer 定义按成员读取收件箱所需的工作区、企业身份与成员编号。
type Viewer struct {
	WorkspaceID string
	IdentityID  string
	UserID      string
}

// ViewerOf 返回成员身份对应的收件箱查看者。
func ViewerOf(identity *servermodels.Identity) Viewer {
	return Viewer{WorkspaceID: identity.Workspace.ID, IdentityID: identity.WorkspaceIdentity.ID, UserID: identity.User.ID}
}

// sqlArg 是查询中查看者编号的占位参数：单人读取为编号值，批量读取为引用展开查看者列的 bun.Safe 片段。
type sqlArg = any

// 批量读取时引用 viewersQuery 展开的查看者列。
var (
	viewerWorkspaceColumn = bun.Safe("viewer.workspace_id")
	viewerIdentityColumn  = bun.Safe("viewer.identity_id")
	viewerUserColumn      = bun.Safe("viewer.user_id")
)

// viewersQuery 把查看者展开为 viewer(workspace_id, identity_id, user_id, position) 行，并对每名查看者横向执行 perViewer；position 从 1 起与 viewers 顺序对应。
func viewersQuery(db bun.IDB, viewers []Viewer, perViewer *bun.SelectQuery) *bun.SelectQuery {
	workspaceIDs, identityIDs, userIDs := make([]string, len(viewers)), make([]string, len(viewers)), make([]string, len(viewers))
	for index, viewer := range viewers {
		workspaceIDs[index], identityIDs[index], userIDs[index] = viewer.WorkspaceID, viewer.IdentityID, viewer.UserID
	}
	return db.NewSelect().
		TableExpr("unnest(?::uuid[], ?::uuid[], ?::uuid[]) WITH ORDINALITY AS viewer(workspace_id, identity_id, user_id, position)",
			pgdialect.Array(workspaceIDs), pgdialect.Array(identityIDs), pgdialect.Array(userIDs)).
		ColumnExpr("viewer.position AS viewer_position, per_viewer.*").
		Join("CROSS JOIN LATERAL (?) AS per_viewer", perViewer)
}

//go:build server

// Package conversationaccess 以可嵌入查询的 SQL 谓词定义成员对会话的阅读、列表展示、管理与发送资格，各入口共用同一组规则。
//
// 规则按会话类别给出，同一会话满足任一类别的条件即具备资格：
//   - 服务会话（承载服务会话的渠道客户会话与 AI 员工会话）：工作区全部成员可阅读、列表展示与管理。
//   - 副驾驶线程：所服务的会话是服务会话时，工作区全部成员可阅读；线程不进入收件箱，不可管理。
//   - 单聊、群聊：当前参与者可阅读、列表展示与管理，群聊解散后同样保留。
//   - AI 员工会话：所属成员且仍是参与者时可阅读、列表展示与管理。
//
// 发送资格另取决于会话、群聊、服务周期与渠道状态，由 CheckSendable 返回结构化原因。
package conversationaccess

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/schema"
)

// Viewer 是判定资格的成员：工作区编号与企业身份编号，取参数值，批量判定时取引用外层列的 bun.Safe 片段。
type Viewer struct {
	WorkspaceID any
	IdentityID  any
}

// ViewerOf 返回成员身份对应的判定对象。
func ViewerOf(identity *servermodels.Identity) Viewer {
	return Viewer{WorkspaceID: identity.Workspace.ID, IdentityID: identity.WorkspaceIdentity.ID}
}

// Kind 是资格规则区分的会话类别。
type Kind string

const (
	// KindService 是承载服务会话的会话。
	KindService Kind = "service"
	// KindCopilot 是服务会话上的副驾驶线程。
	KindCopilot Kind = "copilot"
	// KindDirect 是成员单聊。
	KindDirect Kind = "direct"
	// KindGroup 是群聊。
	KindGroup Kind = "group"
	// KindAgent 是成员与 AI 员工的会话。
	KindAgent Kind = "agent"
)

// readableKinds 是授予阅读资格的会话类别。
var readableKinds = []Kind{KindService, KindCopilot, KindDirect, KindGroup, KindAgent}

// listableKinds 是授予列表展示与管理资格的会话类别，副驾驶线程不进入收件箱、不可置顶。
var listableKinds = []Kind{KindService, KindDirect, KindGroup, KindAgent}

// Readable 返回 alias 会话可由成员阅读的条件；指定 kinds 时只按这些类别判定。
func Readable(viewer Viewer, alias string, kinds ...Kind) schema.QueryWithArgs {
	return granted(viewer, alias, readableKinds, kinds)
}

// Listable 返回 alias 会话可在成员收件箱中列表展示的条件；指定 kinds 时只按这些类别判定。
func Listable(viewer Viewer, alias string, kinds ...Kind) schema.QueryWithArgs {
	return granted(viewer, alias, listableKinds, kinds)
}

// manageable 返回成员可在本人列表中管理 alias 会话（置顶、标记已读）的条件，与列表展示资格一致。
func manageable(viewer Viewer, alias string) schema.QueryWithArgs {
	return granted(viewer, alias, listableKinds, nil)
}

// granted 返回 alias 会话属于工作区且满足 allowed 中任一类别条件的谓词，requested 非空时只取其与 allowed 的交集。
func granted(viewer Viewer, alias string, allowed, requested []Kind) schema.QueryWithArgs {
	kinds := allowed
	if len(requested) > 0 {
		kinds = arr.Intersect(requested, allowed)
	}
	if len(kinds) == 0 {
		return bun.SafeQuery("FALSE")
	}
	// 只判定一个类别时以成员关系集合表达，查询可由成员关系驱动；多个类别以相关子查询逐行判定。
	set := len(kinds) == 1
	conditions := make([]any, 0, len(kinds))
	format := ""
	for index, kind := range kinds {
		if index > 0 {
			format += " OR "
		}
		format += "(?)"
		conditions = append(conditions, kindCondition(viewer, alias, kind, set))
	}
	return bun.SafeQuery("(?.workspace_id = ? AND ("+format+"))", append([]any{bun.Ident(alias), viewer.WorkspaceID}, conditions...)...)
}

// kindCondition 返回 alias 会话属于该类别且成员具备资格的条件，set 为真时成员关系以本人会话集合表达。
func kindCondition(viewer Viewer, alias string, kind Kind, set bool) schema.QueryWithArgs {
	cv := bun.Ident(alias)
	switch kind {
	case KindService:
		return bun.SafeQuery("EXISTS (SELECT 1 FROM service_conversations AS access_svc WHERE access_svc.workspace_id = ?.workspace_id AND access_svc.conversation_id = ?.id)", cv, cv)
	case KindCopilot:
		return bun.SafeQuery(`?.type = ? AND EXISTS (
			SELECT 1 FROM service_copilot_threads AS access_sct
			JOIN service_conversations AS access_served ON access_served.workspace_id = access_sct.workspace_id AND access_served.conversation_id = access_sct.served_conversation_id
			WHERE access_sct.workspace_id = ?.workspace_id AND access_sct.conversation_id = ?.id)`, cv, domain.ConversationTypeCopilot, cv, cv)
	case KindDirect:
		return bun.SafeQuery("?.type = ? AND ?", cv, domain.ConversationTypeDirect, participant(viewer, alias, set))
	case KindGroup:
		return bun.SafeQuery("?.type = ? AND ?", cv, domain.ConversationTypeGroup, participant(viewer, alias, set))
	case KindAgent:
		owned := bun.SafeQuery(`EXISTS (
			SELECT 1 FROM agent_conversations AS access_ac
			WHERE access_ac.workspace_id = ?.workspace_id AND access_ac.conversation_id = ?.id AND access_ac.user_identity_id = ?)`, cv, cv, viewer.IdentityID)
		if set {
			owned = bun.SafeQuery("?.id IN (SELECT access_ac.conversation_id FROM agent_conversations AS access_ac WHERE access_ac.workspace_id = ? AND access_ac.user_identity_id = ?)",
				cv, viewer.WorkspaceID, viewer.IdentityID)
		}
		return bun.SafeQuery("?.type = ? AND ? AND ?", cv, domain.ConversationTypeAgent, owned, participant(viewer, alias, set))
	default:
		return bun.SafeQuery("FALSE")
	}
}

// participant 返回成员是 alias 会话现有参与者的条件，set 为真时以本人参与的会话集合表达。
func participant(viewer Viewer, alias string, set bool) schema.QueryWithArgs {
	cv := bun.Ident(alias)
	if set {
		return bun.SafeQuery(`?.id IN (
			SELECT access_cp.conversation_id FROM conversation_participants AS access_cp
			JOIN chat_subjects AS access_cs ON access_cs.workspace_id = access_cp.workspace_id AND access_cs.id = access_cp.subject_id
			WHERE access_cp.workspace_id = ? AND access_cp.left_at IS NULL AND access_cs.kind = ? AND access_cs.source_id = ?)`,
			cv, viewer.WorkspaceID, domain.ChatSubjectKindWorkspaceIdentity, viewer.IdentityID)
	}
	return bun.SafeQuery(`EXISTS (
		SELECT 1 FROM conversation_participants AS access_cp
		JOIN chat_subjects AS access_cs ON access_cs.workspace_id = access_cp.workspace_id AND access_cs.id = access_cp.subject_id
		WHERE access_cp.workspace_id = ?.workspace_id AND access_cp.conversation_id = ?.id AND access_cp.left_at IS NULL
			AND access_cs.kind = ? AND access_cs.source_id = ?)`, cv, cv, domain.ChatSubjectKindWorkspaceIdentity, viewer.IdentityID)
}

// RequireReadable 以一次 EXISTS 查询确认成员可阅读会话，会话不存在或不可阅读时返回 chatstate.ErrConversationNotFound。
func RequireReadable(ctx context.Context, db bun.IDB, identity *servermodels.Identity, conversationID string, kinds ...Kind) error {
	return requireCondition(ctx, db, identity, conversationID, Readable(ViewerOf(identity), "cv", kinds...))
}

// LockManageable 在调用方事务中对会话取共享锁，再以新的语句复核成员的管理资格，写入本人会话状态的操作借此与成员关系变更串行。
func LockManageable(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, conversationID string) error {
	var id string
	err := tx.NewSelect().TableExpr("conversations AS cv").ColumnExpr("cv.id").
		Where("cv.workspace_id = ? AND cv.id = ?", identity.Workspace.ID, conversationID).
		For("SHARE").Scan(ctx, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return chatstate.ErrConversationNotFound
	}
	if err != nil {
		return fmt.Errorf("lock conversation for access check: %w", err)
	}
	return requireCondition(ctx, tx, identity, conversationID, manageable(ViewerOf(identity), "cv"))
}

// requireCondition 判定指定会话满足 condition，不满足时返回 chatstate.ErrConversationNotFound。
func requireCondition(ctx context.Context, db bun.IDB, identity *servermodels.Identity, conversationID string, condition schema.QueryWithArgs) error {
	allowed, err := db.NewSelect().TableExpr("conversations AS cv").
		Where("cv.workspace_id = ? AND cv.id = ?", identity.Workspace.ID, conversationID).
		Where("?", condition).
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("check conversation access: %w", err)
	}
	if !allowed {
		return chatstate.ErrConversationNotFound
	}
	return nil
}

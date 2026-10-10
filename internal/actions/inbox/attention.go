//go:build server

package inbox

import (
	"context"
	"database/sql"

	"github.com/runforyou-ai/luway/internal/actions/contactname"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/schema"
)

// AttentionMessage 定义计入本人提醒的一条未读消息的通知摘要。
type AttentionMessage struct {
	ID                 string                        `bun:"id"`
	Type               domain.MessageType            `bun:"type"`
	Visibility         domain.MessageVisibility      `bun:"visibility"`
	Body               string                        `bun:"body"`
	AttachmentName     *string                       `bun:"attachment_name"`
	SenderName         *string                       `bun:"sender_name"`
	SenderIdentityType *domain.WorkspaceIdentityType `bun:"sender_identity_type"`
}

// ConversationAttention 定义会话摘要与其中计入本人提醒的未读消息。
type ConversationAttention struct {
	Conversation ConversationSummary
	Messages     []AttentionMessage
}

// attentionScope 表示会话中计入本人提醒的未读消息范围。
type attentionScope int

const (
	// attentionNone 表示不计入提醒。
	attentionNone attentionScope = iota
	// attentionAll 表示全部未读消息计入提醒。
	attentionAll
	// attentionMentions 表示只有提醒本人的未读消息计入提醒。
	attentionMentions
)

// scopeOf 按应用角标口径判断会话的提醒范围：本人已归档的会话不计入，服务会话只在属于本人待处理时全部计入，静音群聊只计入提醒本人的消息，静音的单聊与 AI 聊天不计入。
func scopeOf(summary ConversationSummary) attentionScope {
	switch {
	case summary.ArchivedAt != nil, summary.Service != nil && summary.Pending == nil:
		return attentionNone
	case summary.Type == domain.ConversationTypeGroup && summary.Muted:
		return attentionMentions
	case summary.Muted:
		return attentionNone
	default:
		return attentionAll
	}
}

// unreadMessagesQuery 构造本人在会话 cv 中的未读消息查询：他人发送、本人可见、计入未读的消息类型或发给发起人的服务进度、位于个人状态 state 的已读水位之后。
func unreadMessagesQuery(db bun.IDB, identityID sqlArg) *bun.SelectQuery {
	return db.NewSelect().TableExpr("messages AS unread_msg").
		Join("LEFT JOIN conversation_participants AS sender_cp ON sender_cp.workspace_id = unread_msg.workspace_id AND sender_cp.conversation_id = unread_msg.conversation_id AND sender_cp.id = unread_msg.sender_participant_id").
		Join("LEFT JOIN chat_subjects AS sender_cs ON sender_cs.workspace_id = sender_cp.workspace_id AND sender_cs.id = sender_cp.subject_id").
		Where("unread_msg.workspace_id = cv.workspace_id AND unread_msg.conversation_id = cv.id").
		Where("(unread_msg.type IN (?) OR (unread_msg.type = ? AND unread_msg.visibility = ?)) AND unread_msg.deleted_at IS NULL", bun.List(domain.UnreadMessageTypes), domain.MessageTypeSystem, domain.MessageVisibilityRequester).
		Where("(sender_cs.kind IS DISTINCT FROM ? OR sender_cs.source_id IS DISTINCT FROM ?)", domain.ChatSubjectKindWorkspaceIdentity, identityID).
		Where("unread_msg.message_seq > COALESCE(state.read_seq, 0)").
		Where("?", messagequery.VisibleTo("unread_msg", identityID))
}

// mentionsIdentity 构造未读消息 unread_msg 提醒了指定企业身份或提醒所有人的条件。
func mentionsIdentity(db bun.IDB, identityID sqlArg) schema.QueryWithArgs {
	mentioned := db.NewSelect().TableExpr("message_mentions AS mention").ColumnExpr("1").
		Join("JOIN chat_subjects AS mention_cs ON mention_cs.workspace_id = mention.workspace_id AND mention_cs.id = mention.subject_id").
		Where("mention.workspace_id = unread_msg.workspace_id AND mention.message_id = unread_msg.id").
		Where("mention_cs.kind = ? AND mention_cs.source_id = ?", domain.ChatSubjectKindWorkspaceIdentity, identityID)
	return bun.SafeQuery("(unread_msg.mention_all OR EXISTS (?))", mentioned)
}

// unreadCountsQuery 构造会话 cv 中本人的未读数与提醒本人的未读数。
func unreadCountsQuery(db bun.IDB, identityID sqlArg) *bun.SelectQuery {
	return unreadMessagesQuery(db, identityID).
		ColumnExpr("count(*) AS unread_count").
		ColumnExpr("count(*) FILTER (WHERE ?) AS mentioned_unread_count", mentionsIdentity(db, identityID))
}

// attentionBatchSize 是批量读取消息提醒时每批查询共用的查看者数上限。
const attentionBatchSize = 200

// viewerServiceRow 是批量读取中一名查看者的服务会话摘要行。
type viewerServiceRow struct {
	ViewerPosition int `bun:"viewer_position"`
	serviceConversationRow
}

// viewerDirectRow 是批量读取中一名查看者的单聊摘要行。
type viewerDirectRow struct {
	ViewerPosition int `bun:"viewer_position"`
	directConversationRow
}

// viewerAgentRow 是批量读取中一名查看者的 AI 聊天摘要行。
type viewerAgentRow struct {
	ViewerPosition int `bun:"viewer_position"`
	agentConversationRow
}

// viewerGroupRow 是批量读取中一名查看者的群聊摘要行。
type viewerGroupRow struct {
	ViewerPosition int `bun:"viewer_position"`
	groupConversationRow
}

// viewerPendingRow 是批量读取中一名查看者的待处理条目。
type viewerPendingRow struct {
	ViewerPosition int `bun:"viewer_position"`
	inboxCursorPoint
}

// viewerAttentionMessage 是批量读取中一名查看者的未读消息及其是否提醒本人。
type viewerAttentionMessage struct {
	ViewerPosition int  `bun:"viewer_position"`
	Mentioned      bool `bun:"mentioned"`
	AttentionMessage
}

// ReadMessageAttention 读取单名查看者的消息提醒，口径同 ReadMessageAttentions。
func (q *LoadInboxQuery) ReadMessageAttention(ctx context.Context, viewer Viewer, conversationID, messageID string) (*ConversationAttention, error) {
	attentions, err := q.ReadMessageAttentions(ctx, []Viewer{viewer}, conversationID, messageID)
	if err != nil {
		return nil, err
	}
	return attentions[0], nil
}

// ReadMessageAttentions 在同一只读快照中按查看者分批读取会话摘要，并判断指定消息是否为计入各自提醒的未读消息，结果与 viewers 一一对应：本人的 AI 聊天按聊天口径，其余服务会话按待处理口径；会话不可读时为 nil，消息不计入时 Messages 为空。
func (q *LoadInboxQuery) ReadMessageAttentions(ctx context.Context, viewers []Viewer, conversationID, messageID string) ([]*ConversationAttention, error) {
	if !str.IsUUID(conversationID) || !str.IsUUID(messageID) {
		return nil, ErrQueryInvalid
	}
	attentions := make([]*ConversationAttention, 0, len(viewers))
	if len(viewers) == 0 {
		return attentions, nil
	}
	err := q.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		snapshot := NewLoadInboxQuery(tx)
		for start := 0; start < len(viewers); start += attentionBatchSize {
			batch, err := snapshot.readAttentionBatch(ctx, viewers[start:min(start+attentionBatchSize, len(viewers))], conversationID, messageID)
			if err != nil {
				return err
			}
			attentions = append(attentions, batch...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return attentions, nil
}

// readAttentionBatch 在当前快照中为一批查看者读取会话摘要与指定消息的提醒判断，结果与 viewers 一一对应。
func (q *LoadInboxQuery) readAttentionBatch(ctx context.Context, viewers []Viewer, conversationID, messageID string) ([]*ConversationAttention, error) {
	summaries, err := q.readViewerSummaries(ctx, viewers, conversationID)
	if err != nil {
		return nil, err
	}
	attentions := make([]*ConversationAttention, len(viewers))
	scopes := make([]attentionScope, len(viewers))
	attending := false
	for index, summary := range summaries {
		if summary == nil {
			continue
		}
		attentions[index] = &ConversationAttention{Conversation: *summary, Messages: []AttentionMessage{}}
		scopes[index] = scopeOf(*summary)
		attending = attending || scopes[index] != attentionNone
	}
	if !attending {
		return attentions, nil
	}
	query := unreadMessagesQuery(q.db, viewerIdentityColumn).
		ColumnExpr("unread_msg.id::text AS id, unread_msg.type, unread_msg.visibility, unread_msg.body").
		ColumnExpr("(SELECT ma.name FROM message_attachments AS ma WHERE ma.workspace_id = unread_msg.workspace_id AND ma.message_id = unread_msg.id) AS attachment_name").
		ColumnExpr("CASE WHEN sender_cs.kind = ? THEN "+contactname.Expr("sender_c", "sender_cci.display_name")+" ELSE sender_oi.display_name END AS sender_name", domain.ChatSubjectKindContact).
		ColumnExpr("sender_oi.type AS sender_identity_type").
		ColumnExpr("? AS mentioned", mentionsIdentity(q.db, viewerIdentityColumn)).
		Join("JOIN conversations AS cv ON cv.workspace_id = ? AND cv.id = ?", viewerWorkspaceColumn, conversationID).
		Join("LEFT JOIN conversation_user_states AS state ON state.workspace_id = cv.workspace_id AND state.conversation_id = cv.id AND state.user_id = ?", viewerUserColumn).
		Join("LEFT JOIN workspace_identities AS sender_oi ON sender_oi.workspace_id = sender_cs.workspace_id AND sender_oi.id = sender_cs.source_id AND sender_cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("LEFT JOIN contacts AS sender_c ON sender_c.workspace_id = sender_cs.workspace_id AND sender_c.id = sender_cs.source_id AND sender_cs.kind = ?", domain.ChatSubjectKindContact).
		Join("LEFT JOIN channel_conversations AS cc ON cc.workspace_id = cv.workspace_id AND cc.conversation_id = cv.id").
		Join("LEFT JOIN channel_identities AS sender_cci ON sender_cci.workspace_id = cc.workspace_id AND sender_cci.id = cc.channel_identity_id AND sender_cci.contact_id = sender_c.id").
		Where("unread_msg.id = ?", messageID)
	var messages []viewerAttentionMessage
	if err := viewersQuery(q.db, viewers, query).Scan(ctx, &messages); err != nil {
		return nil, err
	}
	// 全部计入的查看者收下该消息，只计入提醒的查看者在消息提醒本人时收下。
	for _, message := range messages {
		index := message.ViewerPosition - 1
		if scopes[index] == attentionAll || (scopes[index] == attentionMentions && message.Mentioned) {
			attentions[index].Messages = append(attentions[index].Messages, message.AttentionMessage)
		}
	}
	return attentions, nil
}

// readViewerSummaries 在当前快照中为每名查看者读取指定会话的摘要，结果与 viewers 一一对应：本人的 AI 聊天取聊天摘要，其余会话取服务会话视角摘要并附带本人的待处理条目；不可读时为 nil。
func (q *LoadInboxQuery) readViewerSummaries(ctx context.Context, viewers []Viewer, conversationID string) ([]*ConversationSummary, error) {
	workspaceID, identityID, userID := viewerWorkspaceColumn, viewerIdentityColumn, viewerUserColumn
	rows := make([]summaryRows, len(viewers))
	var services []viewerServiceRow
	if err := viewersQuery(q.db, viewers, q.serviceConversationDetailsQuery(workspaceID, identityID, userID).Where("cv.id = ?", conversationID)).Scan(ctx, &services); err != nil {
		return nil, err
	}
	for _, row := range services {
		rows[row.ViewerPosition-1].services = append(rows[row.ViewerPosition-1].services, row.serviceConversationRow)
	}
	var directs []viewerDirectRow
	if err := viewersQuery(q.db, viewers, q.directConversationDetailsQuery(workspaceID, identityID, userID).Where("cv.id = ?", conversationID)).Scan(ctx, &directs); err != nil {
		return nil, err
	}
	for _, row := range directs {
		rows[row.ViewerPosition-1].directs = append(rows[row.ViewerPosition-1].directs, row.directConversationRow)
	}
	var agents []viewerAgentRow
	if err := viewersQuery(q.db, viewers, q.agentConversationDetailsQuery(workspaceID, identityID, userID).Where("cv.id = ?", conversationID)).Scan(ctx, &agents); err != nil {
		return nil, err
	}
	for _, row := range agents {
		rows[row.ViewerPosition-1].agents = append(rows[row.ViewerPosition-1].agents, row.agentConversationRow)
	}
	var groups []viewerGroupRow
	if err := viewersQuery(q.db, viewers, q.groupConversationsQuery(workspaceID, identityID, userID).Where("cv.id = ?", conversationID)).Scan(ctx, &groups); err != nil {
		return nil, err
	}
	for _, row := range groups {
		rows[row.ViewerPosition-1].groups = append(rows[row.ViewerPosition-1].groups, row.groupConversationRow)
	}
	var pendings []viewerPendingRow
	if err := viewersQuery(q.db, viewers, q.pendingCandidates(workspaceID, identityID, LoadInput{Scope: domain.InboxScopePending}).Where("items.id = ?", conversationID)).Scan(ctx, &pendings); err != nil {
		return nil, err
	}
	pending := make([]*PendingSummary, len(viewers))
	for _, row := range pendings {
		pending[row.ViewerPosition-1] = row.pending()
	}
	summaries := make([]*ConversationSummary, len(viewers))
	for index := range viewers {
		summary := rows[index].merge(false)[conversationID]
		if summary == nil || summary.Agent != nil {
			summaries[index] = summary
			continue
		}
		summary = rows[index].merge(true)[conversationID]
		summary.Pending = pending[index]
		summaries[index] = summary
	}
	return summaries, nil
}

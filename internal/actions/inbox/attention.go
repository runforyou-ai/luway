//go:build server

package inbox

import (
	"context"
	"database/sql"

	"github.com/runforyou-ai/cervi/internal/actions/contactname"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/schema"
)

// unreadMessageTypes 是计入未读与提醒的消息类型。
var unreadMessageTypes = []domain.MessageType{domain.MessageTypeText, domain.MessageTypeAttachment, domain.MessageTypeAgentError}

// AttentionMessage 定义计入本人提醒的一条未读消息的通知摘要。
type AttentionMessage struct {
	ID                 string                           `bun:"id"`
	Type               domain.MessageType               `bun:"type"`
	Visibility         domain.MessageVisibility         `bun:"visibility"`
	Body               string                           `bun:"body"`
	AttachmentName     *string                          `bun:"attachment_name"`
	SenderName         *string                          `bun:"sender_name"`
	SenderIdentityType *domain.OrganizationIdentityType `bun:"sender_identity_type"`
}

// ConversationAttention 定义会话摘要与其中计入本人提醒的未读消息，消息按会话顺序排列。
type ConversationAttention struct {
	Conversation ConversationSummary
	Messages     []AttentionMessage
}

// attentionScope 表示会话中计入本人提醒的未读消息范围。
type attentionScope int

const (
	attentionNone attentionScope = iota
	attentionAll
	attentionMentions
)

// scopeOf 按应用角标口径判断会话的提醒范围：服务会话只在属于本人待处理时全部计入，静音群聊只计入提醒本人的消息，静音的单聊与 AI 聊天不计入。
func scopeOf(summary ConversationSummary) attentionScope {
	switch {
	case summary.Service != nil && summary.Pending == nil:
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
func unreadMessagesQuery(db bun.IDB, identityID string) *bun.SelectQuery {
	return db.NewSelect().TableExpr("messages AS unread_msg").
		Join("LEFT JOIN conversation_participants AS sender_cp ON sender_cp.organization_id = unread_msg.organization_id AND sender_cp.conversation_id = unread_msg.conversation_id AND sender_cp.id = unread_msg.sender_participant_id").
		Join("LEFT JOIN chat_subjects AS sender_cs ON sender_cs.organization_id = sender_cp.organization_id AND sender_cs.id = sender_cp.subject_id").
		Where("unread_msg.organization_id = cv.organization_id AND unread_msg.conversation_id = cv.id").
		Where("(unread_msg.type IN (?) OR (unread_msg.type = ? AND unread_msg.visibility = ?)) AND unread_msg.deleted_at IS NULL", bun.In(unreadMessageTypes), domain.MessageTypeSystem, domain.MessageVisibilityRequester).
		Where("(sender_cs.kind IS DISTINCT FROM ? OR sender_cs.source_id IS DISTINCT FROM ?)", domain.ChatSubjectKindOrganizationIdentity, identityID).
		Where("unread_msg.message_seq > COALESCE(state.read_seq, 0)").
		Where("?", messagequery.VisibleTo("unread_msg", identityID))
}

// mentionsIdentity 构造未读消息 unread_msg 提醒了指定企业身份或提醒所有人的条件。
func mentionsIdentity(db bun.IDB, identityID string) schema.QueryWithArgs {
	mentioned := db.NewSelect().TableExpr("message_mentions AS mention").ColumnExpr("1").
		Join("JOIN chat_subjects AS mention_cs ON mention_cs.organization_id = mention.organization_id AND mention_cs.id = mention.subject_id").
		Where("mention.organization_id = unread_msg.organization_id AND mention.message_id = unread_msg.id").
		Where("mention_cs.kind = ? AND mention_cs.source_id = ?", domain.ChatSubjectKindOrganizationIdentity, identityID)
	return bun.SafeQuery("(unread_msg.mention_all OR EXISTS (?))", mentioned)
}

// unreadCountsQuery 构造会话 cv 中本人的未读数与提醒本人的未读数。
func unreadCountsQuery(db bun.IDB, identityID string) *bun.SelectQuery {
	return unreadMessagesQuery(db, identityID).
		ColumnExpr("count(*) AS unread_count").
		ColumnExpr("count(*) FILTER (WHERE ?) AS mentioned_unread_count", mentionsIdentity(db, identityID))
}

// ReadAttention 在同一只读快照中读取会话摘要，以及已知消息之后计入本人提醒的未读消息；未给出已知消息时只判断会话最新一条消息。会话不可读时返回 nil。
func (q *LoadInboxQuery) ReadAttention(ctx context.Context, identity *servermodels.Identity, conversationID, afterMessageID string) (*ConversationAttention, error) {
	if !common.ValidUUID(conversationID) || (afterMessageID != "" && !common.ValidUUID(afterMessageID)) {
		return nil, ErrQueryInvalid
	}
	var attention *ConversationAttention
	err := q.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		snapshot := NewLoadInboxQuery(tx)
		results, err := snapshot.readByIDs(ctx, identity, []string{conversationID}, &LoadInput{Scope: domain.InboxScopePending})
		if err != nil || results[0].Conversation == nil {
			return err
		}
		attention = &ConversationAttention{Conversation: *results[0].Conversation, Messages: []AttentionMessage{}}
		scope := scopeOf(attention.Conversation)
		if scope == attentionNone {
			return nil
		}
		query := unreadMessagesQuery(tx, identity.OrganizationIdentity.ID).
			ColumnExpr("unread_msg.id::text AS id, unread_msg.type, unread_msg.visibility, unread_msg.body").
			ColumnExpr("(SELECT ma.name FROM message_attachments AS ma WHERE ma.organization_id = unread_msg.organization_id AND ma.message_id = unread_msg.id) AS attachment_name").
			ColumnExpr("CASE WHEN sender_cs.kind = ? THEN "+contactname.Expr("sender_c", "sender_cci.display_name")+" ELSE sender_oi.display_name END AS sender_name", domain.ChatSubjectKindContact).
			ColumnExpr("sender_oi.type AS sender_identity_type").
			Join("JOIN conversations AS cv ON cv.organization_id = ? AND cv.id = ?", identity.Organization.ID, conversationID).
			Join("LEFT JOIN conversation_user_states AS state ON state.organization_id = cv.organization_id AND state.conversation_id = cv.id AND state.user_id = ?", identity.User.ID).
			Join("LEFT JOIN organization_identities AS sender_oi ON sender_oi.organization_id = sender_cs.organization_id AND sender_oi.id = sender_cs.source_id AND sender_cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
			Join("LEFT JOIN contacts AS sender_c ON sender_c.organization_id = sender_cs.organization_id AND sender_c.id = sender_cs.source_id AND sender_cs.kind = ?", domain.ChatSubjectKindContact).
			Join("LEFT JOIN channel_conversations AS cc ON cc.organization_id = cv.organization_id AND cc.conversation_id = cv.id").
			Join("LEFT JOIN contact_channel_identities AS sender_cci ON sender_cci.organization_id = cc.organization_id AND sender_cci.id = cc.contact_channel_identity_id AND sender_cci.contact_id = sender_c.id")
		if scope == attentionMentions {
			query = query.Where("?", mentionsIdentity(tx, identity.OrganizationIdentity.ID))
		}
		// 已知消息之后的全部新增按会话顺序返回；没有已知消息时只判断会话最新一条消息。
		if afterMessageID != "" {
			query = query.Where("unread_msg.message_seq > COALESCE((SELECT known.message_seq FROM messages AS known WHERE known.organization_id = cv.organization_id AND known.conversation_id = cv.id AND known.id = ?), 0)", afterMessageID)
		} else {
			query = query.Where("unread_msg.id = cv.last_message_id")
		}
		return query.OrderExpr("unread_msg.message_seq ASC").Scan(ctx, &attention.Messages)
	})
	if err != nil {
		return nil, err
	}
	return attention, nil
}

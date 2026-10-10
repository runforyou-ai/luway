//go:build server

package chatstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/runforyou-ai/luway/internal/actions/notificationtask"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/luway/pkg/searchtext"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// AppendMessage 在调用方事务内锁定会话后追加消息、推进会话版本、维护摘要，登记会话受众通知与该消息的用户通知任务；调用方负责授权及完整发送意图校验。
// 消息表不设唯一约束：会话内序号与幂等复查都在本函数持有的会话行锁内完成；写入时可能新建会话的调用方另需先持有发起方的串行锁（如渠道身份锁）。消息只经由本函数、AppendServiceMessage 或 AppendSystemEvent 写入。
func AppendMessage(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, conversation *servermodels.Conversation, message *servermodels.Message) (*servermodels.Message, bool, error) {
	return AppendServiceMessage(ctx, db, enqueuer, conversation, nil, message)
}

// AppendServiceMessage 与 AppendMessage 相同；session 为调用方已锁定的消息所属周期时，周期随消息推进的字段同步到该内存值，为空时等同 AppendMessage。
func AppendServiceMessage(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, conversation *servermodels.Conversation, session *servermodels.ServiceSession, message *servermodels.Message) (*servermodels.Message, bool, error) {
	appended, inserted, err := appendMessage(ctx, db, conversation, session, message)
	if err != nil || !inserted {
		return appended, inserted, err
	}
	if err := notificationtask.EnqueueMessage(ctx, db, enqueuer, appended); err != nil {
		return nil, false, err
	}
	return appended, true, nil
}

// AppendSystemEvent 追加不计入未读与提醒的系统事件，写入行为与 AppendMessage 相同且不登记用户通知任务；计入提醒的消息须经 AppendMessage 写入。
func AppendSystemEvent(ctx context.Context, db bun.IDB, conversation *servermodels.Conversation, message *servermodels.Message) (*servermodels.Message, bool, error) {
	if domain.CountsTowardUnread(domain.MessageType(message.Type), domain.MessageVisibility(message.Visibility)) {
		return nil, false, errors.New("chatstate: message counting toward attention must be appended with AppendMessage")
	}
	return appendMessage(ctx, db, conversation, nil, message)
}

// appendMessage 在调用方事务内锁定会话后追加消息、推进会话版本、维护摘要并登记会话受众通知，返回消息及是否新写入；session 为调用方已锁定的消息所属周期，可以为空。
func appendMessage(ctx context.Context, db bun.IDB, conversation *servermodels.Conversation, session *servermodels.ServiceSession, message *servermodels.Message) (*servermodels.Message, bool, error) {
	var lockedID string
	if err := db.NewRaw("SELECT id::text FROM conversations WHERE id = ? AND workspace_id = ? FOR UPDATE", conversation.ID, conversation.WorkspaceID).Scan(ctx, &lockedID); err != nil {
		return nil, false, fmt.Errorf("lock appended conversation: %w", err)
	}
	// 幂等重放返回既有消息并保留序号和摘要。
	if message.IdempotencyKey != nil {
		existing := &servermodels.Message{}
		err := db.NewSelect().Model(existing).
			Where("msg.workspace_id = ? AND msg.conversation_id = ? AND msg.idempotency_key = ?", conversation.WorkspaceID, conversation.ID, *message.IdempotencyKey).
			Scan(ctx)
		if err == nil {
			return existing, false, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, false, fmt.Errorf("load appended message: %w", err)
		}
	}
	// 序号与会话版本同句推进。
	if err := db.NewUpdate().Model(conversation).
		Set("last_message_seq = last_message_seq + 1").
		Set("version = version + 1").
		WherePK().Where("workspace_id = ?", conversation.WorkspaceID).
		Returning("last_message_seq, version").Scan(ctx); err != nil {
		return nil, false, fmt.Errorf("allocate message sequence: %w", err)
	}
	message.MessageSeq = conversation.LastMessageSeq
	message.Body = str.Remove(message.Body, "\x00")
	if message.Visibility == "" {
		message.Visibility = string(domain.MessageVisibilityShared)
	}
	// 文本消息按正文生成检索词元；附件消息与翻译发送的文本由调用方生成。
	if message.Type == string(domain.MessageTypeText) && message.SearchVector == "" {
		message.SearchVector = searchtext.Vector(message.Body)
	}
	insert := db.NewInsert().Model(message).
		Column("id", "workspace_id", "conversation_id", "service_session_id", "sender_participant_id", "type", "visibility", "body", "language", "search_vector", "system_event_type", "system_event_payload", "reply_to_message_id", "mention_all", "idempotency_key", "client_message_id", "originated_at", "message_seq", "agent_tool_call_id")
	// 调用方未给出发生时间时取持有会话锁之后的数据库时刻，与消息序号同序。
	if message.OriginatedAt.IsZero() {
		insert = insert.Value("originated_at", "clock_timestamp()")
	}
	if _, err := insert.Returning("*").Exec(ctx); err != nil {
		return nil, false, fmt.Errorf("append conversation message: %w", err)
	}
	// 周期摘要只记录共享的对话消息，系统事件不改变；周期字段随消息在同一事务中经 servicestate 推进。
	if message.ServiceSessionID != nil && message.Visibility == string(domain.MessageVisibilityShared) && message.Type != string(domain.MessageTypeSystem) {
		if err := servicestate.RecordMessage(ctx, db, session, message); err != nil {
			return nil, false, err
		}
	}
	// 活动时间取锁内数据库时钟，保留同会话已提交的较大值；会话摘要与活动时间只随会话各方或发起人可见的消息推进，内部消息与服务周期的流转事件不改变，发给发起人的服务进度推进。
	if message.Visibility != string(domain.MessageVisibilityInternal) &&
		(message.Type != string(domain.MessageTypeSystem) || message.ServiceSessionID == nil || message.Visibility == string(domain.MessageVisibilityRequester)) {
		query := db.NewUpdate().Model(conversation).
			Set("last_activity_at = GREATEST(last_activity_at, clock_timestamp())").
			Set("last_message_id = ?", message.ID).
			Set("last_message_at = ?", message.OriginatedAt).
			WherePK().Where("workspace_id = ?", conversation.WorkspaceID)
		if err := query.Returning("last_activity_at").Scan(ctx); err != nil {
			return nil, false, fmt.Errorf("update conversation summary: %w", err)
		}
	} else if message.Visibility == string(domain.MessageVisibilityInternal) && message.Type != string(domain.MessageTypeSystem) {
		// 内部备注与内部错误只推进内部活动时间，供处理方的服务列表排序。
		if err := db.NewUpdate().Model(conversation).
			Set("last_internal_activity_at = GREATEST(last_internal_activity_at, clock_timestamp())").
			WherePK().Where("workspace_id = ?", conversation.WorkspaceID).
			Returning("last_internal_activity_at").Scan(ctx); err != nil {
			return nil, false, fmt.Errorf("update conversation internal activity: %w", err)
		}
	}
	// 内部备注不登记网站访客受众的变更通知；系统事件全部通知访客，访客据此拉取新事件并同步周期评价状态。
	notifyVisitor := message.Visibility != string(domain.MessageVisibilityInternal) || message.Type == string(domain.MessageTypeSystem)
	// 新的对话消息让本会话中已归档的聊天回到各成员的列表；系统事件与内部消息不改变归档状态。
	if message.Type != string(domain.MessageTypeSystem) && message.Visibility != string(domain.MessageVisibilityInternal) {
		var restored []struct {
			UserID  string `bun:"user_id"`
			Version int64  `bun:"version"`
		}
		if err := db.NewUpdate().Model((*servermodels.ConversationUserState)(nil)).
			Set("archived_at = NULL").Set("version = version + 1").
			Where("workspace_id = ? AND conversation_id = ? AND archived_at IS NOT NULL", conversation.WorkspaceID, conversation.ID).
			Returning("user_id, version").Scan(ctx, &restored); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, false, fmt.Errorf("restore archived conversation: %w", err)
		}
		for _, state := range restored {
			realtime.Notify(ctx, realtime.UserConversationStateChanged(conversation.WorkspaceID, state.UserID, conversation.ID, state.Version))
		}
	}
	// 消息改变时间线，系统事件另按事件类型带上参与方或服务周期变化。
	changes := domain.ConversationChangeTimeline
	if message.SystemEventType != nil {
		changes |= domain.ConversationSystemEventType(*message.SystemEventType).ConversationChanges()
	}
	if err := notifyConversationChanged(ctx, db, conversation, changes, notifyVisitor); err != nil {
		return nil, false, err
	}
	return message, true, nil
}

// TouchConversation 在调用方持有会话锁的事务内推进会话版本，并登记带指定变化类别的会话受众变更通知。
func TouchConversation(ctx context.Context, db bun.IDB, conversation *servermodels.Conversation, changes domain.ConversationChanges) error {
	return touchConversation(ctx, db, conversation, changes, true)
}

// touchConversation 推进会话版本并登记变更通知，notifyVisitor 为真时同时登记网站访客目录受众。
func touchConversation(ctx context.Context, db bun.IDB, conversation *servermodels.Conversation, changes domain.ConversationChanges, notifyVisitor bool) error {
	if err := db.NewUpdate().Model(conversation).
		Set("version = version + 1").
		WherePK().Where("workspace_id = ?", conversation.WorkspaceID).
		Returning("version").Scan(ctx); err != nil {
		return fmt.Errorf("advance conversation version: %w", err)
	}
	return notifyConversationChanged(ctx, db, conversation, changes, notifyVisitor)
}

// TouchMemberConversation 在调用方持有会话锁的事务内推进会话版本，并只登记成员与客服受众的变更通知，用于网站访客看不到的变化。
func TouchMemberConversation(ctx context.Context, db bun.IDB, conversation *servermodels.Conversation, changes domain.ConversationChanges) error {
	return touchConversation(ctx, db, conversation, changes, false)
}

// TouchConversations 批量推进子查询选出的会话版本，按变化类别登记成员、客服与网站访客受众通知，返回已锁定的会话编号。
func TouchConversations(ctx context.Context, db bun.IDB, workspaceID string, conversationIDs *bun.SelectQuery, changes domain.ConversationChanges) ([]string, error) {
	conversations, err := touchConversations(ctx, db, workspaceID, conversationIDs, changes, true)
	if err != nil {
		return nil, err
	}
	return arr.OrEmpty(arr.Map(conversations, func(conversation *servermodels.Conversation) string { return conversation.ID })), nil
}

// touchConversations 按会话 ID 顺序锁定子查询选出的会话，以一条语句推进版本，批量登记变更通知并返回这些会话；notifyVisitor 为真时同时登记网站访客目录受众。
func touchConversations(ctx context.Context, db bun.IDB, workspaceID string, conversationIDs *bun.SelectQuery, changes domain.ConversationChanges, notifyVisitor bool) ([]*servermodels.Conversation, error) {
	var conversations []*servermodels.Conversation
	if err := db.NewRaw(`WITH locked AS (
			SELECT id FROM conversations WHERE workspace_id = ? AND id IN (?) ORDER BY id FOR UPDATE
		)
		UPDATE conversations AS cv SET version = cv.version + 1
		FROM locked WHERE cv.workspace_id = ? AND cv.id = locked.id
		RETURNING cv.id, cv.workspace_id, cv.type, cv.version`, workspaceID, conversationIDs, workspaceID).
		Scan(ctx, &conversations); err != nil {
		return nil, fmt.Errorf("advance conversation versions: %w", err)
	}
	if len(conversations) == 0 {
		return conversations, nil
	}
	return conversations, notifyConversationsChanged(ctx, db, workspaceID, conversations, changes, notifyVisitor)
}

// NotifyConversationChanged 按会话当前版本为成员受众补登记变化类别，不推进版本也不通知网站访客；同一事务内已登记的通知与之合并。
func NotifyConversationChanged(ctx context.Context, db bun.IDB, conversation *servermodels.Conversation, changes domain.ConversationChanges) error {
	return notifyConversationChanged(ctx, db, conversation, changes, false)
}

// notifyConversationChanged 按会话当前版本登记单个会话的变更通知，受众规则同 notifyConversationsChanged。
func notifyConversationChanged(ctx context.Context, db bun.IDB, conversation *servermodels.Conversation, changes domain.ConversationChanges, notifyVisitor bool) error {
	return notifyConversationsChanged(ctx, db, conversation.WorkspaceID, []*servermodels.Conversation{conversation}, changes, notifyVisitor)
}

// notifyConversationsChanged 按会话当前版本批量登记变更通知：客户会话及其 Copilot 线程通知企业客服共享受众，网站客户会话在 notifyVisitor 为真时同时通知所属渠道身份受众，内部会话通知当前真人成员，承载服务会话的 AI 聊天另外通知企业客服共享受众；各类受众按会话批量查询。
func notifyConversationsChanged(ctx context.Context, db bun.IDB, workspaceID string, conversations []*servermodels.Conversation, changes domain.ConversationChanges, notifyVisitor bool) error {
	byID := make(map[string]*servermodels.Conversation, len(conversations))
	var customerIDs, agentIDs, memberIDs []string
	for _, conversation := range conversations {
		byID[conversation.ID] = conversation
		switch conversationType := domain.ConversationType(conversation.Type); conversationType {
		case domain.ConversationTypeChannel, domain.ConversationTypeCopilot:
			realtime.Notify(ctx, realtime.ServiceInboxConversationChanged(workspaceID, conversation.ID, conversationType, conversation.Version, changes))
			if conversationType == domain.ConversationTypeChannel && notifyVisitor {
				customerIDs = append(customerIDs, conversation.ID)
			}
		case domain.ConversationTypeAgent:
			agentIDs = append(agentIDs, conversation.ID)
			memberIDs = append(memberIDs, conversation.ID)
		default:
			memberIDs = append(memberIDs, conversation.ID)
		}
	}
	// 仅网站客户会话按所属渠道身份登记访客目录受众通知。
	if len(customerIDs) > 0 {
		var visitors []struct {
			ConversationID    string `bun:"conversation_id"`
			ChannelIdentityID string `bun:"channel_identity_id"`
		}
		if err := db.NewSelect().TableExpr("channel_conversations AS cc").
			ColumnExpr("cc.conversation_id, ci.id AS channel_identity_id").
			Join("JOIN channel_identities AS ci ON ci.workspace_id = cc.workspace_id AND ci.id = cc.channel_identity_id").
			Join("JOIN channels AS c ON c.workspace_id = ci.workspace_id AND c.id = ci.channel_id AND c.type = ?", domain.ChannelTypeWebsite).
			Where("cc.workspace_id = ? AND cc.conversation_id IN (?)", workspaceID, bun.List(customerIDs)).
			Scan(ctx, &visitors); err != nil {
			return fmt.Errorf("load visitor notification audience: %w", err)
		}
		for _, visitor := range visitors {
			realtime.Notify(ctx, realtime.VisitorDirectoryConversationChanged(workspaceID, visitor.ChannelIdentityID, visitor.ConversationID, byID[visitor.ConversationID].Version))
		}
	}
	// AI 聊天承载服务会话时同时通知企业客服共享受众。
	if len(agentIDs) > 0 {
		var served []string
		if err := db.NewSelect().Model((*servermodels.ServiceConversation)(nil)).Column("svc.conversation_id").
			Where("svc.workspace_id = ? AND svc.conversation_id IN (?)", workspaceID, bun.List(agentIDs)).
			Scan(ctx, &served); err != nil {
			return fmt.Errorf("check service conversation notification audience: %w", err)
		}
		for _, conversationID := range served {
			realtime.Notify(ctx, realtime.ServiceInboxConversationChanged(workspaceID, conversationID, domain.ConversationTypeAgent, byID[conversationID].Version, changes))
		}
	}
	if len(memberIDs) > 0 {
		var members []struct {
			ConversationID string `bun:"conversation_id"`
			UserID         string `bun:"user_id"`
		}
		if err := db.NewSelect().TableExpr("conversation_participants AS cp").
			ColumnExpr("cp.conversation_id, u.id AS user_id").
			Join("JOIN chat_subjects AS cs ON cs.workspace_id = cp.workspace_id AND cs.id = cp.subject_id AND cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
			Join("JOIN users AS u ON u.workspace_id = cs.workspace_id AND u.identity_id = cs.source_id").
			Where("cp.workspace_id = ? AND cp.conversation_id IN (?) AND cp.left_at IS NULL", workspaceID, bun.List(memberIDs)).
			Scan(ctx, &members); err != nil {
			return fmt.Errorf("load conversation notification audience: %w", err)
		}
		for _, member := range members {
			conversation := byID[member.ConversationID]
			realtime.Notify(ctx, realtime.UserConversationChanged(workspaceID, member.UserID, conversation.ID, domain.ConversationType(conversation.Type), conversation.Version, changes))
		}
	}
	return nil
}

//go:build server

package channelinbound

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

const (
	// MaxUnrepliedConversations 是发起人未指定会话时可以新建会话的上限：已有这么多进行中的会话尚未收到客服或 AI 回复时拒绝新建。
	MaxUnrepliedConversations = 3
	// ConflictReasonUnrepliedConversationsExceeded 表示发起人尚未收到回复的会话已达上限。
	ConflictReasonUnrepliedConversationsExceeded = "unreplied_conversations_exceeded"
)

// RetryableConstraintNames 是渠道入站消息事务遇到后可整体重试的唯一约束。
var RetryableConstraintNames = map[string]struct{}{
	serverstorage.UniqueChannelIdentityExternal: {},
	serverstorage.UniqueContactExternalUser:     {},
	serverstorage.UniqueServiceSessionOpen:      {},
	serverstorage.UniqueServiceSessionSequence:  {},
}

// generatedIDs 是一次渠道消息写入可能新建的记录编号。
type generatedIDs struct {
	contact         string
	channelIdentity string
	subject         string
	conversation    string
	participant     string
	serviceSession  string
	message         string
}

// generateIDs 为一次渠道消息写入生成 UUIDv7。
func generateIDs() generatedIDs {
	values := make([]string, 7)
	for index := range values {
		values[index] = uuid.NewV7().String()
	}
	return generatedIDs{
		contact: values[0], channelIdentity: values[1], subject: values[2], conversation: values[3],
		participant: values[4], serviceSession: values[5], message: values[6],
	}
}

// selectSingleConversation 取得渠道身份由同一发起人发起、最近有消息的渠道会话，没有时新建。
func selectSingleConversation(ctx context.Context, db bun.IDB, channel *servermodels.Channel, channelIdentityID, requesterSubjectID, body, conversationID string) (*servermodels.Conversation, bool, error) {
	conversation := &servermodels.Conversation{}
	err := db.NewSelect().Model(conversation).
		Join("JOIN channel_conversations AS cc ON cc.workspace_id = cv.workspace_id AND cc.conversation_id = cv.id").
		Join("JOIN service_conversations AS svc ON svc.workspace_id = cc.workspace_id AND svc.conversation_id = cc.conversation_id").
		Where("cv.workspace_id = ?", channel.WorkspaceID).
		Where("cv.type = ?", domain.ConversationTypeChannel).
		Where("cc.channel_identity_id = ?", channelIdentityID).
		Where("svc.requester_subject_id = ?", requesterSubjectID).
		OrderExpr("cv.last_message_at DESC NULLS LAST, cc.conversation_id DESC").
		Limit(1).
		Scan(ctx)
	if err == nil {
		return conversation, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, fmt.Errorf("load channel identity conversation: %w", err)
	}
	return createConversation(ctx, db, channel, channelIdentityID, requesterSubjectID, body, conversationID)
}

// selectTargetConversation 取得指定渠道会话或创建新的渠道会话。
func selectTargetConversation(ctx context.Context, db bun.IDB, channel *servermodels.Channel, channelIdentityID, requesterSubjectID string, requestedConversationID *string, body, conversationID string) (*servermodels.Conversation, bool, error) {
	if requestedConversationID == nil {
		// 渠道身份已由入站写入锁定，同一发起人的新建请求按顺序计数；只计当前周期进行中且周期内还没有客服或 AI 员工对客回复的会话。
		unreplied, err := db.NewSelect().TableExpr("channel_conversations AS cc").
			Join("JOIN service_conversations AS svc ON svc.conversation_id = cc.conversation_id AND svc.workspace_id = cc.workspace_id").
			Join("JOIN service_sessions AS ss ON ss.service_conversation_id = svc.id AND ss.workspace_id = svc.workspace_id AND ss.status = ?", domain.ServiceSessionStatusOpen).
			Where("cc.workspace_id = ? AND cc.channel_identity_id = ?", channel.WorkspaceID, channelIdentityID).
			Where("NOT ?", messagequery.AgentReplied("ss")).
			Where("NOT ?", messagequery.HumanReplied("ss")).
			Count(ctx)
		if err != nil {
			return nil, false, fmt.Errorf("count unreplied channel conversations: %w", err)
		}
		if unreplied >= MaxUnrepliedConversations {
			return nil, false, &conversationaction.ConflictError{Reason: ConflictReasonUnrepliedConversationsExceeded}
		}
		return createConversation(ctx, db, channel, channelIdentityID, requesterSubjectID, body, conversationID)
	}
	conversation := &servermodels.Conversation{}
	err := db.NewSelect().Model(conversation).
		Join("JOIN channel_conversations AS cc ON cc.workspace_id = cv.workspace_id AND cc.conversation_id = cv.id").
		Where("cv.workspace_id = ?", channel.WorkspaceID).
		Where("cv.id = ?", *requestedConversationID).
		Where("cv.type = ?", domain.ConversationTypeChannel).
		Where("cc.channel_identity_id = ?", channelIdentityID).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, conversationaction.ErrConversationNotFound
	}
	if err != nil {
		return nil, false, fmt.Errorf("load channel conversation: %w", err)
	}
	return conversation, false, nil
}

// createConversation 创建渠道会话、渠道身份关系，以及发起人为渠道身份所属主体、服务对象由渠道类型决定的服务会话。
func createConversation(ctx context.Context, db bun.IDB, channel *servermodels.Channel, channelIdentityID, requesterSubjectID, body, conversationID string) (*servermodels.Conversation, bool, error) {
	// 从首条正文派生稳定会话标题。
	conversation := &servermodels.Conversation{
		ID: conversationID, WorkspaceID: channel.WorkspaceID, Type: string(domain.ConversationTypeChannel),
		Status: string(domain.ConversationStatusActive), Title: new(str.Substr(str.Squish(body), 0, 60)),
	}
	if _, err := db.NewInsert().Model(conversation).
		Column("id", "workspace_id", "type", "status", "title", "created_by_subject_id").
		Exec(ctx); err != nil {
		return nil, false, fmt.Errorf("create channel conversation: %w", err)
	}
	relation := &servermodels.ChannelConversation{ConversationID: conversation.ID, WorkspaceID: channel.WorkspaceID, ChannelIdentityID: channelIdentityID}
	if _, err := db.NewInsert().Model(relation).
		Column("conversation_id", "workspace_id", "channel_identity_id").
		Exec(ctx); err != nil {
		return nil, false, fmt.Errorf("create channel conversation relation: %w", err)
	}
	if err := chatstate.CreateServiceConversation(ctx, db, &servermodels.ServiceConversation{
		WorkspaceID: channel.WorkspaceID, ConversationID: conversation.ID,
		Source: string(domain.ServiceSourceChannel), RequesterSubjectID: requesterSubjectID,
		Audience: string(domain.ChannelCapabilitiesOf(domain.ChannelType(channel.Type)).Audience),
	}); err != nil {
		return nil, false, err
	}
	return conversation, true, nil
}

//go:build server

// Package customernotify 在网站访客转人工后收集邮箱，并在访客未读到客服回复时发送邮件通知。
package customernotify

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"uuid"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	"github.com/runforyou-ai/cervi/internal/actions/contactprofile"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/runforyou-ai/cervi/pkg/email"
	"github.com/runforyou-ai/cervi/pkg/mail"
	"github.com/uptrace/bun"
)

// Sender 发送一封邮件；为空表示部署未配置邮件发送。
type Sender interface {
	Send(ctx context.Context, message mail.Message) error
}

// emailCandidatePattern 匹配正文中形如邮箱地址的片段。
var emailCandidatePattern = regexp.MustCompile(`[^\s<>()\[\]{},;:"'，。；：、（）《》“”‘’]+@[^\s<>()\[\]{},;:"'，。；：、（）《》“”‘’]+`)

// customerRecipient 是客户会话对应的渠道身份与联系人邮箱。
type customerRecipient struct {
	ChannelType       string  `bun:"channel_type"`
	ChannelIdentityID string  `bun:"channel_identity_id"`
	ExternalID        string  `bun:"external_id"`
	ContactID         string  `bun:"contact_id"`
	Email             *string `bun:"email"`
}

// loadCustomerRecipient 读取客户会话的渠道类型、渠道身份与联系人邮箱，主邮箱优先。
func loadCustomerRecipient(ctx context.Context, db bun.IDB, organizationID, conversationID string) (customerRecipient, error) {
	recipient := customerRecipient{}
	err := db.NewSelect().TableExpr("channel_conversations AS cc").
		ColumnExpr("c.type AS channel_type, cci.id AS channel_identity_id, cci.external_id, cci.contact_id").
		ColumnExpr(`(SELECT cm.normalized_value FROM contact_methods AS cm
			WHERE cm.organization_id = cci.organization_id AND cm.contact_id = cci.contact_id AND cm.type = ?
			ORDER BY cm.is_primary DESC, cm.created_at LIMIT 1) AS email`, domain.ContactMethodTypeEmail).
		Join("JOIN contact_channel_identities AS cci ON cci.organization_id = cc.organization_id AND cci.id = cc.contact_channel_identity_id").
		Join("JOIN channels AS c ON c.organization_id = cci.organization_id AND c.id = cci.channel_id").
		Where("cc.organization_id = ? AND cc.conversation_id = ?", organizationID, conversationID).
		Scan(ctx, &recipient)
	if err != nil {
		return customerRecipient{}, fmt.Errorf("load customer email recipient: %w", err)
	}
	return recipient, nil
}

// EmailRequested 判断转人工话术是否请访客留下邮箱：部署已配置发信、网站渠道且联系人没有邮箱。
func EmailRequested(ctx context.Context, db bun.IDB, sender Sender, organizationID, conversationID string) (bool, error) {
	if sender == nil {
		return false, nil
	}
	recipient, err := loadCustomerRecipient(ctx, db, organizationID, conversationID)
	if err != nil {
		return false, err
	}
	return domain.ChannelType(recipient.ChannelType) == domain.ChannelTypeWebsite && recipient.Email == nil, nil
}

// CollectEmail 在调用方持有会话与周期锁的事务中，从转人工或退回队列后、真人回复前的访客消息里识别唯一邮箱并写入联系人，同时写入访客可见的留邮箱事件；返回是否写入。
func CollectEmail(ctx context.Context, db bun.IDB, sender Sender, conversation *servermodels.Conversation, session *servermodels.ServiceSession, body string) (bool, error) {
	if sender == nil || domain.ServiceSessionStatus(session.Status) != domain.ServiceSessionStatusOpen {
		return false, nil
	}
	address, found := extractEmail(body)
	if !found {
		return false, nil
	}
	recipient, err := loadCustomerRecipient(ctx, db, conversation.OrganizationID, conversation.ID)
	if err != nil {
		return false, err
	}
	if domain.ChannelType(recipient.ChannelType) != domain.ChannelTypeWebsite || recipient.Email != nil {
		return false, nil
	}
	awaiting, err := awaitingHumanReply(ctx, db, session)
	if err != nil || !awaiting {
		return false, err
	}
	// 联系方式达到数量上限时邮箱未写入，不追加留邮箱事件。
	added, err := contactprofile.AddMethod(ctx, db, conversation.OrganizationID, recipient.ContactID, domain.ContactMethodTypeEmail, address)
	if err != nil || !added {
		return false, err
	}
	payload, err := json.Marshal(domain.ServiceSessionEmailEvent{ServiceSessionID: session.ID, Email: address})
	if err != nil {
		return false, fmt.Errorf("encode email collected event: %w", err)
	}
	eventType := string(domain.ConversationSystemEventServiceSessionEmailCollected)
	if _, _, err := chatstate.AppendMessage(ctx, db, conversation, &servermodels.Message{
		ID: uuid.NewV7().String(), OrganizationID: conversation.OrganizationID, ConversationID: conversation.ID,
		ServiceSessionID: &session.ID, Type: string(domain.MessageTypeSystem), Visibility: string(domain.MessageVisibilityInternal),
		SystemEventType: &eventType, SystemEventPayload: payload, OriginatedAt: time.Now().UTC(),
	}); err != nil {
		return false, fmt.Errorf("append email collected event: %w", err)
	}
	// 新增邮箱改变客户资料与成员界面名称，推进该联系人全部客户会话的版本。
	if err := chatstate.TouchContactProfileConversations(ctx, db, conversation.OrganizationID, recipient.ContactID); err != nil {
		return false, err
	}
	return true, nil
}

// awaitingHumanReply 判断周期已转人工或退回队列、当前不由 AI 负责，且此后还没有真人对客回复。
func awaitingHumanReply(ctx context.Context, db bun.IDB, session *servermodels.ServiceSession) (bool, error) {
	// 周期未转人工也未退回队列时聚合结果为空。
	var handoffSeq sql.NullInt64
	if err := db.NewSelect().Model((*servermodels.Message)(nil)).ColumnExpr("max(msg.message_seq)").
		Where("msg.organization_id = ? AND msg.conversation_id = ? AND msg.service_session_id = ?", session.OrganizationID, session.ConversationID, session.ID).
		Where("msg.system_event_type IN (?, ?)", domain.ConversationSystemEventServiceSessionHandedOff, domain.ConversationSystemEventServiceSessionReturned).
		Scan(ctx, &handoffSeq); err != nil {
		return false, fmt.Errorf("load service session handoff: %w", err)
	}
	if !handoffSeq.Valid {
		return false, nil
	}
	if session.AssigneeIdentityID != nil {
		var assigneeType string
		if err := db.NewSelect().Model((*servermodels.OrganizationIdentity)(nil)).Column("oi.type").
			Where("oi.organization_id = ? AND oi.id = ?", session.OrganizationID, *session.AssigneeIdentityID).
			Scan(ctx, &assigneeType); err != nil {
			return false, fmt.Errorf("load service session assignee type: %w", err)
		}
		if domain.OrganizationIdentityTypeIsAI(domain.OrganizationIdentityType(assigneeType)) {
			return false, nil
		}
	}
	replied, err := humanRepliesQuery(db, session.OrganizationID, session.ConversationID).
		Where("msg.service_session_id = ? AND msg.message_seq > ?", session.ID, handoffSeq.Int64).
		Exists(ctx)
	if err != nil {
		return false, fmt.Errorf("check human reply after handoff: %w", err)
	}
	return !replied, nil
}

// humanRepliesQuery 返回客户会话中真人成员发出的未删除对客文本与附件消息查询，别名为 msg。
func humanRepliesQuery(db bun.IDB, organizationID, conversationID string) *bun.SelectQuery {
	return db.NewSelect().TableExpr("messages AS msg").
		Join("JOIN conversation_participants AS cp ON cp.organization_id = msg.organization_id AND cp.id = msg.sender_participant_id").
		Join("JOIN chat_subjects AS cs ON cs.organization_id = cp.organization_id AND cs.id = cp.subject_id AND cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("JOIN organization_identities AS oi ON oi.organization_id = cs.organization_id AND oi.id = cs.source_id AND oi.type = ?", domain.OrganizationIdentityTypeUser).
		Where("msg.organization_id = ? AND msg.conversation_id = ?", organizationID, conversationID).
		Where("msg.type IN (?) AND msg.visibility = ? AND msg.deleted_at IS NULL",
			bun.In([]domain.MessageType{domain.MessageTypeText, domain.MessageTypeAttachment}), domain.MessageVisibilityShared)
}

// extractEmail 返回正文中唯一的合法邮箱地址；没有或出现多个不同地址时返回 false。
func extractEmail(body string) (string, bool) {
	found := ""
	for _, candidate := range emailCandidatePattern.FindAllString(body, -1) {
		// 去掉句末标点后按标准格式校验。
		address := email.Normalize(strings.TrimRight(candidate, ".!?。！？"))
		if !email.Valid(address) {
			continue
		}
		if found != "" && found != address {
			return "", false
		}
		found = address
	}
	return found, found != ""
}

//go:build server

package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/contactprofile"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/integration/agentruntime"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// copilotBackgroundWindowPercent 客户会话背景资料最多占模型窗口的百分比。
const copilotBackgroundWindowPercent = 25

type copilotRunPolicy struct{}

// lockContext 锁定 Copilot 线程及其固定 AI 员工的参与关系。
func (p copilotRunPolicy) lockContext(ctx context.Context, db bun.IDB, run *servermodels.AgentRun) (agentRunPolicyContext, error) {
	cv, err := chatstate.LockConversation(ctx, db, run.OrganizationID, run.ConversationID)
	if err != nil {
		return agentRunPolicyContext{}, err
	}
	if cv.Type != string(domain.ConversationTypeCopilot) {
		return agentRunPolicyContext{}, errors.New("agent run does not belong to a copilot thread")
	}
	var participantID string
	if err := db.NewSelect().TableExpr("conversation_participants AS cp").
		ColumnExpr("cp.id").
		Join("JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.organization_id = cp.organization_id AND cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("JOIN service_copilot_threads AS sct ON sct.conversation_id = cp.conversation_id AND sct.organization_id = cp.organization_id AND sct.agent_identity_id = cs.source_id").
		Where("cp.organization_id = ? AND cp.conversation_id = ?", run.OrganizationID, run.ConversationID).
		Where("cp.left_at IS NULL AND sct.agent_identity_id = ?", run.AgentIdentityID).
		For("UPDATE OF cp").Scan(ctx, &participantID); err != nil {
		return agentRunPolicyContext{}, fmt.Errorf("lock copilot thread agent participant: %w", err)
	}
	return agentRunPolicyContext{Conversation: cv, AgentParticipantID: participantID}, nil
}

// historyServiceSession 以线程所属服务会话的当前周期为历史检索锚点。
func (p copilotRunPolicy) historyServiceSession(ctx context.Context, db bun.IDB, run *servermodels.AgentRun) (string, error) {
	var serviceSessionID string
	if err := db.NewSelect().TableExpr("service_copilot_threads AS sct").
		ColumnExpr("svc.current_service_session_id::text").
		Join("JOIN service_conversations AS svc ON svc.organization_id = sct.organization_id AND svc.conversation_id = sct.served_conversation_id").
		Where("sct.organization_id = ? AND sct.conversation_id = ?", run.OrganizationID, run.ConversationID).
		Scan(ctx, &serviceSessionID); err != nil {
		return "", fmt.Errorf("load copilot customer service session: %w", err)
	}
	return serviceSessionID, nil
}

// prepareLocked 确认 Copilot 线程的 AI 员工可以继续处理已提交的提问。
func (p copilotRunPolicy) prepareLocked(context.Context, bun.IDB, agentRunPolicyContext, *servermodels.AgentRun) (bool, error) {
	return true, nil
}

// loadMessages 读取带提问人的线程历史，并在本次认领的首条提问之前放入所属客户会话的最新背景资料。
func (p copilotRunPolicy) loadMessages(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, endSeq int64, links serverfilecontent.Links) ([]agentruntime.Message, error) {
	history, err := loadClaimedConversationMessages(ctx, db, run, endSeq, links, true)
	if err != nil {
		return nil, err
	}
	background, err := loadCopilotBackground(ctx, db, run, links)
	if err != nil {
		return nil, err
	}
	claimed, err := loadClaimedInputMessages(ctx, db, run, endSeq)
	if err != nil {
		return nil, err
	}
	// 背景资料紧邻本次认领的提问，按模型窗口截取较早历史时仍然保留。
	index := slices.IndexFunc(history, func(message agentruntime.Message) bool { return claimed[message.ID] })
	if index < 0 {
		index = len(history)
	}
	return slices.Insert(history, index, background), nil
}

// persistMessage 以 AI 员工参与者身份追加线程回复。
func (p copilotRunPolicy) persistMessage(ctx context.Context, db bun.IDB, policyContext agentRunPolicyContext, run *servermodels.AgentRun, messageID string, messageType domain.MessageType, content string) error {
	_, _, err := appendAgentMessage(ctx, db, policyContext.Conversation, agentResultMessage(run, messageID, policyContext.AgentParticipantID, messageType, content, nil))
	return err
}

// sceneContext 给出协助客服的场景。
func (p copilotRunPolicy) sceneContext(context.Context, bun.IDB, executionContext) (agentruntime.SceneContext, error) {
	return agentruntime.SceneContext{Scene: agentruntime.SceneCopilot}, nil
}

// laneRevision 读取线程固定 AI 员工当前生效的配置版本。
func (p copilotRunPolicy) laneRevision(ctx context.Context, db bun.IDB, policyContext agentRunPolicyContext, lane *servermodels.AgentLane) (string, bool, error) {
	return agentChatRunPolicy{}.laneRevision(ctx, db, policyContext, lane)
}

type copilotBackground struct {
	Kind           string                                `json:"kind"`
	Contact        string                                `json:"contact"`
	Profile        *copilotBackgroundProfile             `json:"profile,omitempty"`
	Channel        *copilotBackgroundChannel             `json:"channel,omitempty"`
	ServiceSession copilotBackgroundSession              `json:"serviceSession"`
	History        []agentruntime.CustomerHistorySummary `json:"history,omitempty"`
	Messages       []copilotBackgroundMessage            `json:"messages"`
}

type copilotBackgroundProfile struct {
	agentruntime.CustomerProfile
	Email string `json:"email,omitempty"`
	Phone string `json:"phone,omitempty"`
}

type copilotBackgroundChannel struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type copilotBackgroundSession struct {
	Status   string              `json:"status"`
	Assignee *groupMessageSender `json:"assignee,omitempty"`
}

type copilotBackgroundMessage struct {
	Sender     groupMessageSender       `json:"sender"`
	Visibility domain.MessageVisibility `json:"visibility"`
	Body       string                   `json:"body"`
	SentAt     time.Time                `json:"sentAt"`
	Attachment *contextAttachment       `json:"attachment,omitempty"`
	ReplyTo    *claimedMessageReference `json:"replyTo,omitempty"`
}

type copilotBackgroundRow struct {
	ID                 string                   `bun:"id"`
	Visibility         domain.MessageVisibility `bun:"visibility"`
	Body               string                   `bun:"body"`
	OriginatedAt       time.Time                `bun:"originated_at"`
	SenderKind         string                   `bun:"sender_kind"`
	SenderIdentityType string                   `bun:"sender_identity_type"`
	SenderName         string                   `bun:"sender_name"`
	ReplyToMessageID   *string                  `bun:"reply_to_message_id"`
	ReplyBody          string                   `bun:"reply_body"`
	ReplySenderName    string                   `bun:"reply_sender_name"`
	ReplyDeleted       bool                     `bun:"reply_deleted"`
	contextAttachmentRow
}

// loadCopilotBackground 读取所属客户会话的客户、渠道、当前客服周期、同一客户最近的历史小结和最近沟通记录，沟通记录按模型窗口预算保留较新的部分。
func loadCopilotBackground(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, links serverfilecontent.Links) (agentruntime.Message, error) {
	var header struct {
		ServedConversationID string  `bun:"served_conversation_id"`
		ServiceSessionID     string  `bun:"service_session_id"`
		Version              int64   `bun:"version"`
		ContactName          string  `bun:"contact_name"`
		ContactID            *string `bun:"contact_id"`
		ContactEmail         *string `bun:"contact_email"`
		ContactPhone         *string `bun:"contact_phone"`
		ChannelType          *string `bun:"channel_type"`
		ChannelName          *string `bun:"channel_name"`
		SessionStatus        string  `bun:"session_status"`
		AssigneeName         *string `bun:"assignee_name"`
		AssigneeType         *string `bun:"assignee_type"`
		ContextWindow        int64   `bun:"context_window"`
	}
	if err := db.NewSelect().TableExpr("service_copilot_threads AS sct").
		ColumnExpr("svc.conversation_id::text AS served_conversation_id, svc.current_service_session_id::text AS service_session_id, cv.version").
		ColumnExpr("COALESCE(cci.display_name, c.display_name, requester_oi.display_name, '') AS contact_name").
		ColumnExpr("c.id::text AS contact_id").
		ColumnExpr("(SELECT cm.value FROM contact_methods AS cm WHERE cm.organization_id = c.organization_id AND cm.contact_id = c.id AND cm.type = ? ORDER BY cm.is_primary DESC, cm.created_at ASC LIMIT 1) AS contact_email", domain.ContactMethodTypeEmail).
		ColumnExpr("(SELECT cm.value FROM contact_methods AS cm WHERE cm.organization_id = c.organization_id AND cm.contact_id = c.id AND cm.type = ? ORDER BY cm.is_primary DESC, cm.created_at ASC LIMIT 1) AS contact_phone", domain.ContactMethodTypePhone).
		ColumnExpr("ch.type AS channel_type, ch.name AS channel_name").
		ColumnExpr("ss.status AS session_status, assignee.display_name AS assignee_name, assignee.type AS assignee_type").
		ColumnExpr("COALESCE(aipm.context_window, 0) AS context_window").
		Join("JOIN service_conversations AS svc ON svc.organization_id = sct.organization_id AND svc.conversation_id = sct.served_conversation_id").
		Join("JOIN conversations AS cv ON cv.organization_id = svc.organization_id AND cv.id = svc.conversation_id").
		Join("JOIN chat_subjects AS requester_cs ON requester_cs.id = svc.requester_subject_id AND requester_cs.organization_id = svc.organization_id").
		Join("LEFT JOIN contacts AS c ON c.id = requester_cs.source_id AND c.organization_id = requester_cs.organization_id AND requester_cs.kind = ?", domain.ChatSubjectKindContact).
		Join("LEFT JOIN organization_identities AS requester_oi ON requester_oi.id = requester_cs.source_id AND requester_oi.organization_id = requester_cs.organization_id AND requester_cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("LEFT JOIN channel_conversations AS cc ON cc.organization_id = svc.organization_id AND cc.conversation_id = svc.conversation_id").
		Join("LEFT JOIN contact_channel_identities AS cci ON cci.id = cc.contact_channel_identity_id AND cci.organization_id = cc.organization_id").
		Join("LEFT JOIN channels AS ch ON ch.id = cci.channel_id AND ch.organization_id = cci.organization_id").
		Join("JOIN service_sessions AS ss ON ss.id = svc.current_service_session_id AND ss.organization_id = svc.organization_id AND ss.service_conversation_id = svc.id").
		Join("LEFT JOIN organization_identities AS assignee ON assignee.organization_id = ss.organization_id AND assignee.id = ss.assignee_identity_id").
		Join("LEFT JOIN agent_revisions AS ar ON ar.organization_id = sct.organization_id AND ar.id = ?", run.AgentRevisionID).
		Join("LEFT JOIN ai_provider_models AS aipm ON aipm.organization_id = ar.organization_id AND aipm.provider_id = (ar.configuration->'model'->>'providerId')::uuid AND aipm.identifier = ar.configuration->'model'->>'identifier'").
		Where("sct.organization_id = ? AND sct.conversation_id = ?", run.OrganizationID, run.ConversationID).
		Scan(ctx, &header); err != nil {
		return agentruntime.Message{}, fmt.Errorf("load copilot customer conversation background: %w", err)
	}
	rows := make([]copilotBackgroundRow, 0, agentHistoryLimit)
	if err := db.NewSelect().TableExpr("messages AS msg").
		ColumnExpr("msg.id, msg.visibility, msg.body, msg.originated_at, cs.kind AS sender_kind, COALESCE(oi.type, '') AS sender_identity_type").
		ColumnExpr("COALESCE(CASE WHEN cs.kind = ? THEN COALESCE(cci.display_name, c.display_name) ELSE oi.display_name END, '') AS sender_name", domain.ChatSubjectKindContact).
		ColumnExpr("msg.reply_to_message_id").
		ColumnExpr("? AS reply_body", messagequery.Summary("reply")).
		ColumnExpr("COALESCE(reply_oi.display_name, reply_c.display_name, '') AS reply_sender_name").
		ColumnExpr("reply.deleted_at IS NOT NULL AS reply_deleted").
		Join("JOIN conversation_participants AS cp ON cp.id = msg.sender_participant_id AND cp.organization_id = msg.organization_id AND cp.conversation_id = msg.conversation_id").
		Join("JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.organization_id = cp.organization_id").
		Join("LEFT JOIN organization_identities AS oi ON oi.id = cs.source_id AND oi.organization_id = cs.organization_id AND cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("LEFT JOIN channel_conversations AS cc ON cc.conversation_id = msg.conversation_id AND cc.organization_id = msg.organization_id").
		Join("LEFT JOIN contact_channel_identities AS cci ON cci.id = cc.contact_channel_identity_id AND cci.organization_id = cc.organization_id AND cci.contact_id = cs.source_id AND cs.kind = ?", domain.ChatSubjectKindContact).
		Join("LEFT JOIN contacts AS c ON c.id = cs.source_id AND c.organization_id = cs.organization_id AND cs.kind = ?", domain.ChatSubjectKindContact).
		Join("LEFT JOIN messages AS reply ON reply.id = msg.reply_to_message_id AND reply.organization_id = msg.organization_id AND reply.conversation_id = msg.conversation_id AND reply.type IN (?, ?)", domain.MessageTypeText, domain.MessageTypeAttachment).
		Join("LEFT JOIN conversation_participants AS reply_cp ON reply_cp.id = reply.sender_participant_id AND reply_cp.organization_id = reply.organization_id AND reply_cp.conversation_id = reply.conversation_id").
		Join("LEFT JOIN chat_subjects AS reply_cs ON reply_cs.id = reply_cp.subject_id AND reply_cs.organization_id = reply_cp.organization_id").
		Join("LEFT JOIN organization_identities AS reply_oi ON reply_oi.id = reply_cs.source_id AND reply_oi.organization_id = reply_cs.organization_id AND reply_cs.kind = ?", domain.ChatSubjectKindOrganizationIdentity).
		Join("LEFT JOIN contacts AS reply_c ON reply_c.id = reply_cs.source_id AND reply_c.organization_id = reply_cs.organization_id AND reply_cs.kind = ?", domain.ChatSubjectKindContact).
		Apply(withContextAttachments).
		Where("msg.organization_id = ? AND msg.conversation_id = ?", run.OrganizationID, header.ServedConversationID).
		Where("msg.deleted_at IS NULL").
		OrderExpr("msg.message_seq DESC").
		Limit(agentHistoryLimit).
		Scan(ctx, &rows); err != nil {
		return agentruntime.Message{}, fmt.Errorf("load copilot customer conversation messages: %w", err)
	}
	background := copilotBackground{
		Kind: "customer_conversation_background", Contact: header.ContactName,
		ServiceSession: copilotBackgroundSession{Status: header.SessionStatus},
		Messages:       make([]copilotBackgroundMessage, 0, len(rows)),
	}
	// 发起人是外部联系人时附上客户档案与主要联系方式。
	if header.ContactID != nil {
		profile, err := contactprofile.LoadAgentProfile(ctx, db, run.OrganizationID, *header.ContactID)
		if err != nil {
			return agentruntime.Message{}, err
		}
		background.Profile = &copilotBackgroundProfile{CustomerProfile: profile}
		if header.ContactEmail != nil {
			background.Profile.Email = *header.ContactEmail
		}
		if header.ContactPhone != nil {
			background.Profile.Phone = *header.ContactPhone
		}
	}
	if header.ChannelType != nil && header.ChannelName != nil {
		background.Channel = &copilotBackgroundChannel{Type: *header.ChannelType, Name: *header.ChannelName}
	}
	if header.AssigneeName != nil && header.AssigneeType != nil {
		background.ServiceSession.Assignee = &groupMessageSender{Name: *header.AssigneeName, Kind: *header.AssigneeType}
	}
	history, err := servicesummary.RecentHistory(ctx, db, run.OrganizationID, header.ServiceSessionID, nil)
	if err != nil {
		return agentruntime.Message{}, err
	}
	background.History = history
	// 历史小结与沟通记录共用背景预算；由新到旧累计沟通记录的 Token 估算，超出预算后停止，最新一条始终保留。
	budget := agentruntime.ContextWindowTokens(agentruntime.ModelConfig{ContextWindow: int(header.ContextWindow)}) * copilotBackgroundWindowPercent / 100
	used := 0
	for _, entry := range history {
		used += agentruntime.EstimateTextTokens(entry.Summary)
	}
	for _, row := range rows {
		item := copilotBackgroundMessage{
			Sender: groupMessageSender{Name: row.SenderName, Kind: "member"}, Visibility: row.Visibility,
			Body: row.Body, SentAt: row.OriginatedAt, Attachment: row.attachment(row.ID, links),
		}
		switch {
		case domain.ChatSubjectKind(row.SenderKind) == domain.ChatSubjectKindContact:
			item.Sender.Kind = "customer"
		case domain.OrganizationIdentityType(row.SenderIdentityType) == domain.OrganizationIdentityTypeAgent:
			item.Sender.Kind = "agent"
		}
		if row.ReplyToMessageID != nil {
			item.ReplyTo = &claimedMessageReference{MessageID: *row.ReplyToMessageID, Deleted: row.ReplyDeleted}
			if !row.ReplyDeleted {
				item.ReplyTo.SenderName, item.ReplyTo.Body = row.ReplySenderName, row.ReplyBody
				item.ReplyTo.Attachment = row.replyAttachment(links)
			}
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return agentruntime.Message{}, fmt.Errorf("encode copilot background message: %w", err)
		}
		used += agentruntime.EstimateTextTokens(string(encoded))
		if used > budget && len(background.Messages) > 0 {
			break
		}
		background.Messages = append(background.Messages, item)
	}
	slices.Reverse(background.Messages)
	encoded, err := json.Marshal(background)
	if err != nil {
		return agentruntime.Message{}, fmt.Errorf("encode copilot background: %w", err)
	}
	// 客户会话版本变化时背景资料取得新编号，同一运行内的后续认领据此补入最新背景。
	return agentruntime.Message{
		ID:   fmt.Sprintf("copilot-background:%s:%d", header.ServedConversationID, header.Version),
		Role: agentruntime.MessageRoleUser, Content: string(encoded),
	}, nil
}

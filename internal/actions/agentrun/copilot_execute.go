//go:build server

package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/runforyou-ai/einorun/llm"
	"github.com/runforyou-ai/luway/internal/actions/agentmessage"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/contactprofile"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// copilotBackgroundWindowPercent 是客户会话背景资料最多占模型窗口的百分比。
const copilotBackgroundWindowPercent = 25

// copilotRunPolicy 定义 Copilot 线程的运行策略。
type copilotRunPolicy struct {
	enqueuer servertask.TxEnqueuer
}

// lockContext 锁定 Copilot 线程及其固定 AI 员工的参与关系。
func (p copilotRunPolicy) lockContext(ctx context.Context, db bun.IDB, run *servermodels.AgentRun) (agentRunPolicyContext, error) {
	cv, err := chatstate.LockConversation(ctx, db, run.WorkspaceID, run.ConversationID)
	if err != nil {
		return agentRunPolicyContext{}, err
	}
	if cv.Type != string(domain.ConversationTypeCopilot) {
		return agentRunPolicyContext{}, errors.New("agent run does not belong to a copilot thread")
	}
	var participantID string
	if err := db.NewSelect().TableExpr("conversation_participants AS cp").
		ColumnExpr("cp.id").
		Join("JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.workspace_id = cp.workspace_id AND cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("JOIN service_copilot_threads AS sct ON sct.conversation_id = cp.conversation_id AND sct.workspace_id = cp.workspace_id AND sct.agent_identity_id = cs.source_id").
		Where("cp.workspace_id = ? AND cp.conversation_id = ?", run.WorkspaceID, run.ConversationID).
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
		Join("JOIN service_conversations AS svc ON svc.workspace_id = sct.workspace_id AND svc.conversation_id = sct.served_conversation_id").
		Where("sct.workspace_id = ? AND sct.conversation_id = ?", run.WorkspaceID, run.ConversationID).
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
func (p copilotRunPolicy) loadMessages(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, endSeq int64, links serverfilecontent.Links) ([]agentcontract.Message, error) {
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
	index := slices.IndexFunc(history, func(message agentcontract.Message) bool { return claimed.Has(message.ID) })
	if index < 0 {
		index = len(history)
	}
	return slices.Insert(history, index, background), nil
}

// persistMessage 以 AI 员工参与者身份追加线程回复。
func (p copilotRunPolicy) persistMessage(ctx context.Context, db bun.IDB, policyContext agentRunPolicyContext, run *servermodels.AgentRun, messageID string, messageType domain.MessageType, content string) error {
	_, _, err := agentmessage.Append(ctx, db, p.enqueuer, policyContext.Conversation, agentResultMessage(run, messageID, policyContext.AgentParticipantID, messageType, content, nil))
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

// copilotBackground 是交给模型的所属客户会话背景资料。
type copilotBackground struct {
	Kind           string                                 `json:"kind"`
	Contact        string                                 `json:"contact"`
	Profile        *copilotBackgroundProfile              `json:"profile,omitempty"`
	Channel        *copilotBackgroundChannel              `json:"channel,omitempty"`
	ServiceSession copilotBackgroundSession               `json:"serviceSession"`
	History        []agentcontract.CustomerHistorySummary `json:"history,omitempty"`
	Messages       []copilotBackgroundMessage             `json:"messages"`
}

// copilotBackgroundProfile 是背景资料中的客户档案与主要联系方式。
type copilotBackgroundProfile struct {
	agentcontract.CustomerProfile
	Email string `json:"email,omitempty"`
	Phone string `json:"phone,omitempty"`
}

// copilotBackgroundChannel 是背景资料中客户会话所在的渠道。
type copilotBackgroundChannel struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

// copilotBackgroundSession 是背景资料中当前客服周期的状态与负责人。
type copilotBackgroundSession struct {
	Status   string              `json:"status"`
	Assignee *groupMessageSender `json:"assignee,omitempty"`
}

// copilotBackgroundMessage 是背景资料中的一条沟通记录。
type copilotBackgroundMessage struct {
	Sender     groupMessageSender       `json:"sender"`
	Visibility domain.MessageVisibility `json:"visibility"`
	Body       string                   `json:"body"`
	SentAt     time.Time                `json:"sentAt"`
	Attachment *contextAttachment       `json:"attachment,omitempty"`
	ReplyTo    *claimedMessageReference `json:"replyTo,omitempty"`
}

// copilotBackgroundRow 是读取客户会话沟通记录的一行。
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
func loadCopilotBackground(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, links serverfilecontent.Links) (agentcontract.Message, error) {
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
		ColumnExpr("COALESCE(ci.display_name, c.display_name, requester_oi.display_name, '') AS contact_name").
		ColumnExpr("c.id::text AS contact_id").
		ColumnExpr("c.primary_email AS contact_email").
		ColumnExpr("(SELECT cm.value FROM contact_methods AS cm WHERE cm.workspace_id = c.workspace_id AND cm.contact_id = c.id AND cm.type = ? ORDER BY cm.is_primary DESC, cm.created_at ASC LIMIT 1) AS contact_phone", domain.ContactMethodTypePhone).
		ColumnExpr("ch.type AS channel_type, ch.name AS channel_name").
		ColumnExpr("ss.status AS session_status, assignee.display_name AS assignee_name, assignee.type AS assignee_type").
		ColumnExpr("COALESCE(aim.context_window, 0) AS context_window").
		Join("JOIN service_conversations AS svc ON svc.workspace_id = sct.workspace_id AND svc.conversation_id = sct.served_conversation_id").
		Join("JOIN conversations AS cv ON cv.workspace_id = svc.workspace_id AND cv.id = svc.conversation_id").
		Join("JOIN chat_subjects AS requester_cs ON requester_cs.id = svc.requester_subject_id AND requester_cs.workspace_id = svc.workspace_id").
		Join("LEFT JOIN contacts AS c ON c.id = requester_cs.source_id AND c.workspace_id = requester_cs.workspace_id AND requester_cs.kind = ?", domain.ChatSubjectKindContact).
		Join("LEFT JOIN workspace_identities AS requester_oi ON requester_oi.id = requester_cs.source_id AND requester_oi.workspace_id = requester_cs.workspace_id AND requester_cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("LEFT JOIN channel_conversations AS cc ON cc.workspace_id = svc.workspace_id AND cc.conversation_id = svc.conversation_id").
		Join("LEFT JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id AND ci.workspace_id = cc.workspace_id").
		Join("LEFT JOIN channels AS ch ON ch.id = ci.channel_id AND ch.workspace_id = ci.workspace_id").
		Join("JOIN service_sessions AS ss ON ss.id = svc.current_service_session_id AND ss.workspace_id = svc.workspace_id AND ss.service_conversation_id = svc.id").
		Join("LEFT JOIN workspace_identities AS assignee ON assignee.workspace_id = ss.workspace_id AND assignee.id = ss.assignee_identity_id").
		Join("LEFT JOIN agent_revisions AS ar ON ar.workspace_id = sct.workspace_id AND ar.id = ?", run.AgentRevisionID).
		Join("LEFT JOIN ai_models AS aim ON aim.id = ar.model_id").
		Where("sct.workspace_id = ? AND sct.conversation_id = ?", run.WorkspaceID, run.ConversationID).
		Scan(ctx, &header); err != nil {
		return agentcontract.Message{}, fmt.Errorf("load copilot customer conversation background: %w", err)
	}
	rows := make([]copilotBackgroundRow, 0, agentHistoryLimit)
	if err := db.NewSelect().TableExpr("messages AS msg").
		ColumnExpr("msg.id, msg.visibility, msg.body, msg.originated_at, cs.kind AS sender_kind, COALESCE(oi.type, '') AS sender_identity_type").
		ColumnExpr("COALESCE(CASE WHEN cs.kind = ? THEN COALESCE(ci.display_name, c.display_name) ELSE oi.display_name END, '') AS sender_name", domain.ChatSubjectKindContact).
		ColumnExpr("msg.reply_to_message_id").
		ColumnExpr("? AS reply_body", messagequery.Summary("reply")).
		ColumnExpr("COALESCE(reply_oi.display_name, reply_c.display_name, '') AS reply_sender_name").
		ColumnExpr("reply.deleted_at IS NOT NULL AS reply_deleted").
		Join("JOIN conversation_participants AS cp ON cp.id = msg.sender_participant_id AND cp.workspace_id = msg.workspace_id AND cp.conversation_id = msg.conversation_id").
		Join("JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.workspace_id = cp.workspace_id").
		Join("LEFT JOIN workspace_identities AS oi ON oi.id = cs.source_id AND oi.workspace_id = cs.workspace_id AND cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("LEFT JOIN channel_conversations AS cc ON cc.conversation_id = msg.conversation_id AND cc.workspace_id = msg.workspace_id").
		Join("LEFT JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id AND ci.workspace_id = cc.workspace_id AND ci.contact_id = cs.source_id AND cs.kind = ?", domain.ChatSubjectKindContact).
		Join("LEFT JOIN contacts AS c ON c.id = cs.source_id AND c.workspace_id = cs.workspace_id AND cs.kind = ?", domain.ChatSubjectKindContact).
		Join("LEFT JOIN messages AS reply ON reply.id = msg.reply_to_message_id AND reply.workspace_id = msg.workspace_id AND reply.conversation_id = msg.conversation_id AND reply.type IN (?, ?)", domain.MessageTypeText, domain.MessageTypeAttachment).
		Join("LEFT JOIN conversation_participants AS reply_cp ON reply_cp.id = reply.sender_participant_id AND reply_cp.workspace_id = reply.workspace_id AND reply_cp.conversation_id = reply.conversation_id").
		Join("LEFT JOIN chat_subjects AS reply_cs ON reply_cs.id = reply_cp.subject_id AND reply_cs.workspace_id = reply_cp.workspace_id").
		Join("LEFT JOIN workspace_identities AS reply_oi ON reply_oi.id = reply_cs.source_id AND reply_oi.workspace_id = reply_cs.workspace_id AND reply_cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("LEFT JOIN contacts AS reply_c ON reply_c.id = reply_cs.source_id AND reply_c.workspace_id = reply_cs.workspace_id AND reply_cs.kind = ?", domain.ChatSubjectKindContact).
		Apply(withContextAttachments).
		Where("msg.workspace_id = ? AND msg.conversation_id = ?", run.WorkspaceID, header.ServedConversationID).
		Where("msg.deleted_at IS NULL").
		OrderExpr("msg.message_seq DESC").
		Limit(agentHistoryLimit).
		Scan(ctx, &rows); err != nil {
		return agentcontract.Message{}, fmt.Errorf("load copilot customer conversation messages: %w", err)
	}
	background := copilotBackground{
		Kind: "customer_conversation_background", Contact: header.ContactName,
		ServiceSession: copilotBackgroundSession{Status: header.SessionStatus},
		Messages:       make([]copilotBackgroundMessage, 0, len(rows)),
	}
	// 发起人是外部联系人时附上客户档案与主要联系方式。
	if header.ContactID != nil {
		profile, err := contactprofile.LoadAgentProfile(ctx, db, run.WorkspaceID, *header.ContactID)
		if err != nil {
			return agentcontract.Message{}, err
		}
		background.Profile = &copilotBackgroundProfile{CustomerProfile: profile}
		background.Profile.Email = support.Deref(header.ContactEmail)
		background.Profile.Phone = support.Deref(header.ContactPhone)
	}
	if header.ChannelType != nil && header.ChannelName != nil {
		background.Channel = &copilotBackgroundChannel{Type: *header.ChannelType, Name: *header.ChannelName}
	}
	if header.AssigneeName != nil && header.AssigneeType != nil {
		background.ServiceSession.Assignee = &groupMessageSender{Name: *header.AssigneeName, Kind: *header.AssigneeType}
	}
	history, err := servicesummary.RecentHistory(ctx, db, run.WorkspaceID, header.ServiceSessionID, nil)
	if err != nil {
		return agentcontract.Message{}, err
	}
	background.History = history
	// 历史小结与沟通记录共用背景预算；由新到旧累计沟通记录的 Token 估算，超出预算后停止，最新一条始终保留。
	budget := llm.ContextWindow(int(header.ContextWindow)) * copilotBackgroundWindowPercent / 100
	used := arr.SumBy(history, func(entry agentcontract.CustomerHistorySummary) int {
		return llm.EstimateTokens(entry.Summary)
	})
	for _, row := range rows {
		item := copilotBackgroundMessage{
			Sender: groupMessageSender{Name: row.SenderName, Kind: "member"}, Visibility: row.Visibility,
			Body: row.Body, SentAt: row.OriginatedAt, Attachment: row.attachment(ctx, row.ID, links),
		}
		switch {
		case domain.ChatSubjectKind(row.SenderKind) == domain.ChatSubjectKindContact:
			item.Sender.Kind = "customer"
		case domain.WorkspaceIdentityType(row.SenderIdentityType) == domain.WorkspaceIdentityTypeAgent:
			item.Sender.Kind = "agent"
		}
		if row.ReplyToMessageID != nil {
			item.ReplyTo = &claimedMessageReference{MessageID: *row.ReplyToMessageID, Deleted: row.ReplyDeleted}
			if !row.ReplyDeleted {
				item.ReplyTo.SenderName, item.ReplyTo.Body = row.ReplySenderName, row.ReplyBody
				item.ReplyTo.Attachment = row.replyAttachment(ctx, links)
			}
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return agentcontract.Message{}, fmt.Errorf("encode copilot background message: %w", err)
		}
		used += llm.EstimateTokens(string(encoded))
		if used > budget && len(background.Messages) > 0 {
			break
		}
		background.Messages = append(background.Messages, item)
	}
	slices.Reverse(background.Messages)
	encoded, err := json.Marshal(background)
	if err != nil {
		return agentcontract.Message{}, fmt.Errorf("encode copilot background: %w", err)
	}
	// 客户会话版本变化时背景资料取得新编号，同一运行内的后续认领据此补入最新背景。
	return agentcontract.Message{
		ID:   fmt.Sprintf("copilot-background:%s:%d", header.ServedConversationID, header.Version),
		Role: agentcontract.MessageRoleUser, Content: string(encoded),
	}, nil
}

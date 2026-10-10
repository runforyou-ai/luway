//go:build server

package agentrun

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/runforyou-ai/luway/internal/actions/agentmessage"
	"github.com/runforyou-ai/luway/internal/actions/agentprocess"
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/contactprofile"
	"github.com/runforyou-ai/luway/internal/actions/servicecategory"
	servicesessionaction "github.com/runforyou-ai/luway/internal/actions/servicesession"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/agentcontract"
	"github.com/runforyou-ai/luway/internal/agentruntime"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	serverfilecontent "github.com/runforyou-ai/luway/internal/storage/server/filecontent"
	"github.com/runforyou-ai/luway/internal/storage/server/messagequery"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/uptrace/bun"
)

// customerRunPolicy 定义服务周期执行范围的运行策略。
type customerRunPolicy struct {
	enqueuer servertask.TxEnqueuer
}

// lockContext 锁定 AI 员工所属服务会话的当前服务周期，渠道来源另外取得外发路由。
func (p customerRunPolicy) lockContext(ctx context.Context, db bun.IDB, run *servermodels.AgentRun) (agentRunPolicyContext, error) {
	service, err := chatstate.LoadServiceConversation(ctx, db, run.WorkspaceID, run.ConversationID)
	if err != nil {
		return agentRunPolicyContext{}, err
	}
	source := domain.ServiceSource(service.Source)
	// 经外部平台投递的渠道先锁渠道和渠道身份，再锁会话，协调配置、入站和人工回复。
	var route deliveryaction.Route
	if source == domain.ServiceSourceChannel {
		if route, err = deliveryaction.Prepare(ctx, db, run.WorkspaceID, run.ConversationID); err != nil {
			return agentRunPolicyContext{}, err
		}
	}
	conversation, err := chatstate.LockServiceConversation(ctx, db, run.WorkspaceID, run.ConversationID)
	if err != nil {
		return agentRunPolicyContext{}, err
	}
	session, err := chatstate.LockCurrentServiceSession(ctx, db, run.WorkspaceID, run.ConversationID)
	if err != nil {
		return agentRunPolicyContext{}, err
	}
	return agentRunPolicyContext{Conversation: conversation, ServiceSession: session, ServiceSource: source, DeliveryRoute: route}, nil
}

// historyServiceSession 返回客户历史检索锚点：服务客户的会话取运行所属的服务周期，其他会话为空。
func (p customerRunPolicy) historyServiceSession(ctx context.Context, db bun.IDB, run *servermodels.AgentRun) (string, error) {
	service, err := chatstate.LoadServiceConversation(ctx, db, run.WorkspaceID, run.ConversationID)
	if err != nil {
		return "", err
	}
	if domain.ServiceAudience(service.Audience) != domain.ServiceAudienceCustomer {
		return "", nil
	}
	return run.ScopeID, nil
}

// prepareLocked 校验客户运行仍属于当前负责人，并收敛已经失效的运行。
func (p customerRunPolicy) prepareLocked(ctx context.Context, db bun.IDB, policyContext agentRunPolicyContext, run *servermodels.AgentRun) (bool, error) {
	// 校验运行仍属于当前开放周期和有效 AI 客服。
	session := policyContext.ServiceSession
	eligible := false
	if run.ScopeID == session.ID &&
		domain.ServiceSessionStatus(session.Status) == domain.ServiceSessionStatusOpen &&
		session.AssigneeIdentityID != nil && *session.AssigneeIdentityID == run.AgentIdentityID {
		_, current, err := loadCustomerAgentEligibility(ctx, db, session, run.AgentRevisionID)
		if err != nil {
			return false, err
		}
		eligible = current
	}
	if eligible {
		return true, nil
	}
	if err := suppressCustomerRun(ctx, db, run, policyContext.ServiceSession); err != nil {
		return false, err
	}
	return false, nil
}

// loadMessages 读取本轮服务周期内的模型上下文：服务客户的会话开头是客户身份与访问上下文，本轮最后认领的输入是超时跟进时在末尾追加系统跟进提示。
func (p customerRunPolicy) loadMessages(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, endSeq int64, links serverfilecontent.Links) ([]agentcontract.Message, error) {
	messages, err := loadClaimedCustomerMessages(ctx, db, run, endSeq, links)
	if err != nil {
		return nil, err
	}
	service, err := chatstate.LoadServiceConversation(ctx, db, run.WorkspaceID, run.ConversationID)
	if err != nil {
		return nil, err
	}
	if domain.ServiceAudience(service.Audience) == domain.ServiceAudienceCustomer {
		customer, err := loadCustomerContextMessage(ctx, db, run)
		if err != nil {
			return nil, err
		}
		messages = append([]agentcontract.Message{customer}, messages...)
	}
	input, err := loadLastClaimedInput(ctx, db, run, endSeq)
	if err != nil {
		return nil, err
	}
	if domain.AgentInputKind(input.Kind) == domain.AgentInputKindFollowUp {
		messages = append(messages, agentcontract.Message{ID: "follow-up:" + input.ID, Role: agentcontract.MessageRoleUser, Content: agentruntime.CustomerIdleMessage})
	}
	return messages, nil
}

// applyDecision 按客服运行的结束方式更新周期：回应客户消息时客户确认解决且没有待处理输入则关闭周期；请求确认解决或回应超时跟进时记录请求时间，跟进轮的结束语同样只记为确认请求。
func (p customerRunPolicy) applyDecision(ctx context.Context, db bun.IDB, policyContext agentRunPolicyContext, run *servermodels.AgentRun, lane *servermodels.AgentLane, result agentruntime.RunResult, messageID string) error {
	session := policyContext.ServiceSession
	input, err := loadLastClaimedInput(ctx, db, run, result.EndSeq)
	if err != nil {
		return err
	}
	followUp := domain.AgentInputKind(input.Kind) == domain.AgentInputKindFollowUp
	if result.Decision.Kind == domain.AgentRunOutcomeResolve && !followUp {
		// 结束语之后仍有客户输入待处理时保持周期开放，由下一次运行继续处理。
		if lane.DesiredSeq > result.EndSeq {
			return nil
		}
		return servicesessionaction.CloseAgentServiceSession(ctx, db, p.enqueuer, policyContext.Conversation, session, policyContext.ServiceSource, domain.ServiceSessionCloseAIResolved)
	}
	requested := followUp ||
		(result.Decision.Kind == domain.AgentRunOutcomeAskCustomer && result.Decision.Purpose == domain.AgentAskCustomerPurposeConfirmResolution)
	if !requested {
		return nil
	}
	now, err := serverstorage.Now(ctx, db)
	if err != nil {
		return err
	}
	return servicestate.Begin(session).RequestResolution(messageID, now).Save(ctx, db, p.enqueuer)
}

// loadLastClaimedInput 读取运行在指定输入边界内最后认领的输入。
func loadLastClaimedInput(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, endSeq int64) (*servermodels.AgentInput, error) {
	input := &servermodels.AgentInput{}
	if err := db.NewSelect().Model(input).
		Column("id", "kind").
		Where("lane_id = ? AND agent_run_id = ? AND input_seq <= ?", run.LaneID, run.ID, endSeq).
		OrderExpr("input_seq DESC").
		Limit(1).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("load last claimed agent input: %w", err)
	}
	return input, nil
}

// persistMessage 追加客服 Agent 结果并记录有效首响。
func (p customerRunPolicy) persistMessage(ctx context.Context, db bun.IDB, policyContext agentRunPolicyContext, run *servermodels.AgentRun, messageID string, messageType domain.MessageType, content string) error {
	participantID, err := agentmessage.EnsureCustomerParticipant(ctx, db, run.WorkspaceID, run.ConversationID, run.AgentIdentityID)
	if err != nil {
		return err
	}
	_, err = agentmessage.AppendCustomer(ctx, db, p.enqueuer, policyContext.Conversation, policyContext.ServiceSession, policyContext.DeliveryRoute, agentResultMessage(run, messageID, participantID, messageType, content, &policyContext.ServiceSession.ID))
	return err
}

// sceneContext 按服务会话的服务对象给出客户服务或员工服务场景与企业咨询分类目录。
func (p customerRunPolicy) sceneContext(ctx context.Context, db bun.IDB, execution executionContext) (agentruntime.SceneContext, error) {
	service, err := chatstate.LoadServiceConversation(ctx, db, execution.Run.WorkspaceID, execution.Run.ConversationID)
	if err != nil {
		return agentruntime.SceneContext{}, err
	}
	scene := agentruntime.SceneCustomer
	if domain.ServiceAudience(service.Audience) != domain.ServiceAudienceCustomer {
		scene = agentruntime.SceneEmployeeService
	}
	categories, err := servicecategory.Active(ctx, db, execution.Run.WorkspaceID)
	if err != nil {
		return agentruntime.SceneContext{}, err
	}
	handoffCategories := arr.Map(categories, func(category servermodels.ServiceCategory) agentruntime.HandoffCategory {
		return agentruntime.HandoffCategory{ID: category.ID, Name: category.Name, Description: category.Description}
	})
	return agentruntime.SceneContext{Scene: scene, HandoffCategories: handoffCategories}, nil
}

// laneRevision 在当前负责人仍合格时返回客户 Agent 的配置版本。
func (p customerRunPolicy) laneRevision(ctx context.Context, db bun.IDB, policyContext agentRunPolicyContext, lane *servermodels.AgentLane) (string, bool, error) {
	if policyContext.ServiceSession.AssigneeIdentityID == nil || *policyContext.ServiceSession.AssigneeIdentityID != lane.AgentIdentityID {
		return "", false, nil
	}
	eligibility, eligible, err := loadCustomerAgentEligibility(ctx, db, policyContext.ServiceSession, "")
	if err != nil || !eligible {
		return "", false, err
	}
	return eligibility.RevisionID, true, nil
}

// customerMessageRow 是读取服务周期上下文的一行：消息、平台侧引用、会话内引用与附件。
type customerMessageRow struct {
	ExternalReplyID          *string `bun:"external_reply_id"`
	ExternalReplyBody        string  `bun:"external_reply_body"`
	ExternalReplySenderName  string  `bun:"external_reply_sender_name"`
	ExternalReplySenderIsBot bool    `bun:"external_reply_sender_is_bot"`
	ID                       string  `bun:"id"`
	ReplyToMessageID         *string `bun:"reply_to_message_id"`
	ReplyDeleted             bool    `bun:"reply_deleted"`
	ReplyBody                string  `bun:"reply_body"`
	ReplySenderKind          string  `bun:"reply_sender_kind"`
	ReplySenderID            string  `bun:"reply_sender_id"`
	ReplySenderName          string  `bun:"reply_sender_name"`
	Body                     string  `bun:"body"`
	FromRequester            bool    `bun:"from_requester"`
	contextAttachmentRow
}

// customerMessageReference 是服务周期上下文消息结构化正文中的一层引用，External 表示引用来自外部平台。
type customerMessageReference struct {
	MessageID      string             `json:"messageId,omitempty"`
	External       bool               `json:"external,omitempty"`
	SenderIsBot    bool               `json:"senderIsBot,omitempty"`
	Deleted        bool               `json:"deleted,omitempty"`
	SenderKind     string             `json:"senderKind,omitempty"`
	SenderSourceID string             `json:"senderSourceId,omitempty"`
	SenderName     string             `json:"senderName,omitempty"`
	Body           string             `json:"body,omitempty"`
	Attachment     *contextAttachment `json:"attachment,omitempty"`
}

// loadClaimedCustomerMessages 读取本轮客服周期内不越过已认领输入的消息，并入提交给本 AI 员工的工具调用结果事件。
func loadClaimedCustomerMessages(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, endSeq int64, links serverfilecontent.Links) ([]agentcontract.Message, error) {
	boundary, err := loadClaimedMessageBoundary(ctx, db, run, endSeq)
	if err != nil {
		return nil, err
	}
	messages, err := loadServiceSessionMessages(ctx, db, run.WorkspaceID, run.ConversationID, run.ScopeID, boundary.MessageSeq, links)
	if err != nil {
		return nil, err
	}
	return mergeToolCallEvents(ctx, db, run, messages, boundary.MessageSeq)
}

// loadServiceSessionMessages 读取服务周期内不越过指定消息序号的最近共享消息，发起人发言投影为 user，处理方发言投影为 assistant。
func loadServiceSessionMessages(ctx context.Context, db bun.IDB, workspaceID, conversationID, serviceSessionID string, throughSeq int64, links serverfilecontent.Links) ([]agentcontract.Message, error) {
	rows := make([]customerMessageRow, 0, agentHistoryLimit)
	// 仅筛选主消息的客服周期；当前消息主动引用的旧周期原文仍作为一层引用传入。
	if err := db.NewSelect().
		TableExpr("messages AS msg").
		ColumnExpr("msg.id, msg.body, cp.subject_id = svc.requester_subject_id AS from_requester").
		ColumnExpr("msg.reply_to_message_id").
		ColumnExpr("cm.reply_provider_message_id AS external_reply_id, cm.reply_body AS external_reply_body, cm.reply_sender_name AS external_reply_sender_name, cm.reply_sender_is_bot AS external_reply_sender_is_bot").
		Join("LEFT JOIN channel_messages AS cm ON cm.message_id = msg.id AND cm.part = 1 AND cm.workspace_id = msg.workspace_id AND cm.conversation_id = msg.conversation_id").
		ColumnExpr("reply.deleted_at IS NOT NULL AS reply_deleted").
		ColumnExpr("? AS reply_body", messagequery.Summary("reply")).
		ColumnExpr("reply_cs.kind AS reply_sender_kind, reply_cs.source_id AS reply_sender_id").
		ColumnExpr("CASE WHEN reply_cs.kind = ? THEN COALESCE(reply_cci.display_name, reply_c.display_name) ELSE reply_oi.display_name END AS reply_sender_name", domain.ChatSubjectKindContact).
		Join("JOIN conversation_participants AS cp ON cp.id = msg.sender_participant_id AND cp.workspace_id = msg.workspace_id AND cp.conversation_id = msg.conversation_id").
		Join("JOIN chat_subjects AS cs ON cs.id = cp.subject_id AND cs.workspace_id = cp.workspace_id").
		Join("JOIN service_conversations AS svc ON svc.workspace_id = msg.workspace_id AND svc.conversation_id = msg.conversation_id").
		Join("LEFT JOIN messages AS reply ON reply.id = msg.reply_to_message_id AND reply.workspace_id = msg.workspace_id AND reply.conversation_id = msg.conversation_id AND reply.type IN (?, ?)", domain.MessageTypeText, domain.MessageTypeAttachment).
		Join("LEFT JOIN conversation_participants AS reply_cp ON reply_cp.id = reply.sender_participant_id AND reply_cp.workspace_id = reply.workspace_id AND reply_cp.conversation_id = reply.conversation_id").
		Join("LEFT JOIN chat_subjects AS reply_cs ON reply_cs.id = reply_cp.subject_id AND reply_cs.workspace_id = reply_cp.workspace_id").
		Join("LEFT JOIN workspace_identities AS reply_oi ON reply_oi.id = reply_cs.source_id AND reply_oi.workspace_id = reply_cs.workspace_id AND reply_cs.kind = ?", domain.ChatSubjectKindWorkspaceIdentity).
		Join("LEFT JOIN channel_conversations AS cc ON cc.conversation_id = msg.conversation_id AND cc.workspace_id = msg.workspace_id").
		Join("LEFT JOIN channel_identities AS reply_cci ON reply_cci.id = cc.channel_identity_id AND reply_cci.workspace_id = cc.workspace_id AND reply_cci.contact_id = reply_cs.source_id AND reply_cs.kind = ?", domain.ChatSubjectKindContact).
		Join("LEFT JOIN contacts AS reply_c ON reply_c.id = reply_cs.source_id AND reply_c.workspace_id = reply_cs.workspace_id AND reply_cs.kind = ?", domain.ChatSubjectKindContact).
		Where("msg.workspace_id = ?", workspaceID).
		Where("msg.conversation_id = ?", conversationID).
		Apply(withContextAttachments).
		Where("msg.service_session_id = ?", serviceSessionID).
		Where("msg.visibility = ?", domain.MessageVisibilityShared).
		Where("msg.deleted_at IS NULL").
		Where("cs.kind IN (?, ?)", domain.ChatSubjectKindContact, domain.ChatSubjectKindWorkspaceIdentity).
		Where("msg.message_seq <= ?", throughSeq).
		OrderExpr("msg.message_seq DESC").
		Limit(agentHistoryLimit).
		Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load claimed customer conversation context: %w", err)
	}
	slices.Reverse(rows)
	messages := make([]agentcontract.Message, 0, len(rows))
	for _, row := range rows {
		role := agentcontract.MessageRoleAssistant
		if row.FromRequester {
			role = agentcontract.MessageRoleUser
		}
		content := row.Body
		attachment := row.attachment(ctx, row.ID, links)
		// 在同一模型消息中保留附件描述、一层引用原文和主体类型。
		if row.ReplyToMessageID != nil || row.ExternalReplyID != nil || attachment != nil {
			var replyTo *customerMessageReference
			if row.ReplyToMessageID != nil || row.ExternalReplyID != nil {
				replyTo = &customerMessageReference{Deleted: row.ReplyDeleted}
				replyTo.MessageID = support.Deref(row.ReplyToMessageID)
				if !row.ReplyDeleted {
					replyTo.Body, replyTo.SenderKind = row.ReplyBody, row.ReplySenderKind
					replyTo.SenderSourceID, replyTo.SenderName = row.ReplySenderID, row.ReplySenderName
					replyTo.Attachment = row.replyAttachment(ctx, links)
				}
				if row.ReplyToMessageID == nil {
					replyTo.External = true
					replyTo.Body, replyTo.SenderName, replyTo.SenderIsBot = row.ExternalReplyBody, row.ExternalReplySenderName, row.ExternalReplySenderIsBot
				}
			}
			encoded, _ := json.Marshal(struct {
				Body       string                    `json:"body"`
				Attachment *contextAttachment        `json:"attachment,omitempty"`
				ReplyTo    *customerMessageReference `json:"replyTo,omitempty"`
			}{Body: row.Body, Attachment: attachment, ReplyTo: replyTo})
			content = string(encoded)
		}
		messages = append(messages, agentcontract.Message{ID: row.ID, Revision: row.revision(), Role: role, Content: content, Media: row.media()})
	}
	return messages, nil
}

// suppressCustomerRun 把资格变化后的客户运行收敛为取消终态。
func suppressCustomerRun(ctx context.Context, db bun.IDB, run *servermodels.AgentRun, session *servermodels.ServiceSession) error {
	var errorCode any
	lastError := "customer agent eligibility changed"
	switch {
	case domain.ServiceSessionStatus(session.Status) != domain.ServiceSessionStatusOpen:
		errorCode = domain.AgentRunErrorCodeSessionClosed
		lastError = "customer service session closed"
	case run.ScopeID != session.ID ||
		session.AssigneeIdentityID == nil || *session.AssigneeIdentityID != run.AgentIdentityID:
		errorCode = domain.AgentRunErrorCodeAssigneeChanged
		lastError = "customer service assignee changed"
	default:
		errorCode = nil
	}
	_, err := db.NewUpdate().Model(run).
		Set("status = ?", domain.AgentRunStatusCancelled).
		Set("error_code = ?", errorCode).
		Set("last_error = ?", lastError).
		Set("completed_at = now()").
		WherePK().
		Where("status IN (?)", bun.List(domain.AgentRunActiveStatuses)).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("suppress customer agent run: %w", err)
	}
	return agentprocess.SettleEndedRuns(ctx, db, run.WorkspaceID, run.ID)
}

// loadCustomerContextMessage 读取运行所属客服周期的客户上下文并投影为系统提供的上下文消息；内容变化时修订随之变化。
func loadCustomerContextMessage(ctx context.Context, db bun.IDB, run *servermodels.AgentRun) (agentcontract.Message, error) {
	content, err := CustomerContextContent(ctx, db, run.WorkspaceID, run.ScopeID, nil)
	if err != nil {
		return agentcontract.Message{}, err
	}
	return agentcontract.Message{ID: "customer-context:" + run.ScopeID, Revision: content, Role: agentcontract.MessageRoleUser, Content: content}, nil
}

// CustomerContextContent 读取渠道来源客服周期的客户身份、访客上下文、客户档案与同一客户最近的历史小结，返回客服运行开头的客户上下文正文；closedBefore 非空时历史小结只取在该时间之前关闭的周期。
// 提供是否已验证身份、名称、本次访问信息与客户档案（含内部备注），不含企业用户编号、联系方式与签名身份。
func CustomerContextContent(ctx context.Context, db bun.IDB, workspaceID, serviceSessionID string, closedBefore *time.Time) (string, error) {
	row := struct {
		ContactID      string                 `bun:"contact_id"`
		VerifiedUserID *string                `bun:"verified_user_id"`
		Name           *string                `bun:"name"`
		VisitorContext *domain.VisitorContext `bun:"visitor_context,type:jsonb"`
	}{}
	if err := db.NewSelect().
		TableExpr("service_sessions AS ss").
		ColumnExpr("c.id::text AS contact_id, ci.verified_user_id, ss.visitor_context").
		ColumnExpr("COALESCE(ci.display_name, c.display_name) AS name").
		Join("JOIN channel_conversations AS cc ON cc.workspace_id = ss.workspace_id AND cc.conversation_id = ss.conversation_id").
		Join("JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id AND ci.workspace_id = cc.workspace_id").
		Join("JOIN contacts AS c ON c.id = ci.contact_id AND c.workspace_id = ci.workspace_id").
		Where("ss.workspace_id = ?", workspaceID).
		Where("ss.id = ?", serviceSessionID).
		Scan(ctx, &row); err != nil {
		return "", fmt.Errorf("load customer context: %w", err)
	}
	customer := agentruntime.CustomerContext{
		IdentityVerified: row.VerifiedUserID != nil, Name: support.Deref(row.Name),
	}
	if visit := row.VisitorContext; visit != nil {
		customer.Visit = &agentruntime.CustomerVisit{
			PageURL: visit.PageURL, PageTitle: visit.PageTitle, Language: visit.Language, TimeZone: visit.TimeZone, Country: visit.Country,
		}
	}
	history, err := servicesummary.RecentHistory(ctx, db, workspaceID, serviceSessionID, closedBefore)
	if err != nil {
		return "", err
	}
	customer.History = history
	profile, err := contactprofile.LoadAgentProfile(ctx, db, workspaceID, row.ContactID)
	if err != nil {
		return "", err
	}
	customer.Profile = &profile
	return customer.Message(), nil
}

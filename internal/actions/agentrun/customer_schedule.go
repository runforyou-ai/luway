//go:build server

package agentrun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"uuid"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/domain"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/uptrace/bun"
)

type customerAgentEligibility struct {
	RevisionID string `bun:"revision_id"`
}

// ScheduleCustomerAuto 把一条服务会话发起人的消息追加到当前负责 AI 员工的持久输入流。
func (s *Scheduler) ScheduleCustomerAuto(ctx context.Context, db bun.IDB, organizationID, conversationID, serviceSessionID, messageID string) (bool, error) {
	if s.enqueuer == nil {
		return false, errors.New("agent run scheduler is unavailable")
	}
	locked, err := chatstate.LockServiceSession(ctx, db, organizationID, conversationID)
	if err != nil {
		return false, err
	}
	session := locked.Session
	if session.ID != serviceSessionID {
		return false, errors.New("customer agent service session is no longer current")
	}
	if domain.ServiceSessionStatus(session.Status) != domain.ServiceSessionStatusOpen || session.AssigneeIdentityID == nil {
		return false, nil
	}
	senderSubjectID, err := loadCustomerInputSender(ctx, db, session, messageID)
	if err != nil {
		return false, err
	}
	if senderSubjectID == "" {
		return false, errors.New("customer agent input message is invalid")
	}
	assigneeType, err := loadCustomerAssigneeType(ctx, db, session)
	if err != nil {
		return false, err
	}
	if assigneeType != domain.OrganizationIdentityTypeAgent {
		return false, nil
	}
	eligibility, eligible, err := loadCustomerAgentEligibility(ctx, db, session, "")
	if err != nil {
		return false, err
	}
	if !eligible {
		// 负责人是已失去接待资格的 AI 员工时，在本次入站事务内把周期退回原队列。
		return false, s.returnIneligibleAgentSession(ctx, db, session, "returned:"+session.ID+":"+messageID)
	}

	if err := s.appendInput(ctx, db, agentRunSpec{
		OrganizationID: organizationID, ConversationID: conversationID,
		AgentIdentityID: *session.AssigneeIdentityID, RevisionID: eligibility.RevisionID,
		ScopeKind: domain.AgentExecutionScopeServiceSession, ScopeID: session.ID,
		Kind: domain.AgentInputKindCustomerAuto, SourceSubjectID: senderSubjectID,
	}, messageID); err != nil {
		return false, err
	}
	return true, nil
}

// ScheduleCustomerFollowUp 在调用方已锁定会话的事务内为 AI 员工负责的开放周期追加一次超时跟进输入，来源消息为周期最后一条对客消息；负责人已失去接待资格时把周期退回原队列。
func (s *Scheduler) ScheduleCustomerFollowUp(ctx context.Context, db bun.IDB, session *servermodels.ServiceSession) (bool, error) {
	if s.enqueuer == nil {
		return false, errors.New("agent run scheduler is unavailable")
	}
	if domain.ServiceSessionStatus(session.Status) != domain.ServiceSessionStatusOpen || session.AssigneeIdentityID == nil {
		return false, nil
	}
	eligibility, eligible, err := loadCustomerAgentEligibility(ctx, db, session, "")
	if err != nil {
		return false, err
	}
	if !eligible {
		// 负责人已失去接待资格时，在本次跟进事务内把周期退回原队列。
		return false, s.returnIneligibleAgentSession(ctx, db, session, "returned:"+session.ID+":follow-up:"+session.LastMessageID)
	}
	subject, err := chatstate.EnsureOrganizationIdentityChatSubject(ctx, db, session.OrganizationID, *session.AssigneeIdentityID, uuid.NewV7().String())
	if err != nil {
		return false, err
	}
	if err := s.appendInput(ctx, db, agentRunSpec{
		OrganizationID: session.OrganizationID, ConversationID: session.ConversationID,
		AgentIdentityID: *session.AssigneeIdentityID, RevisionID: eligibility.RevisionID,
		ScopeKind: domain.AgentExecutionScopeServiceSession, ScopeID: session.ID,
		Kind: domain.AgentInputKindFollowUp, SourceSubjectID: subject.ID,
	}, session.LastMessageID); err != nil {
		return false, err
	}
	return true, nil
}

// returnIneligibleAgentSession 在调用方已锁定会话的事务内把失去接待资格的负责人所负责的周期退回队列，key 为退回事件的幂等键。
func (s *Scheduler) returnIneligibleAgentSession(ctx context.Context, db bun.IDB, session *servermodels.ServiceSession, key string) error {
	assignee := &servermodels.OrganizationIdentity{}
	if err := db.NewSelect().Model(assignee).
		Column("oi.id", "oi.type", "oi.display_name").
		Where("oi.organization_id = ? AND oi.id = ?", session.OrganizationID, *session.AssigneeIdentityID).
		Scan(ctx); err != nil {
		return fmt.Errorf("load unavailable customer agent identity: %w", err)
	}
	cancelled, err := returnUnavailableAssigneeSession(ctx, db, s.enqueuer, session.OrganizationID, session.ConversationID, session.ID, assignee, key)
	if err != nil {
		return err
	}
	slog.Warn("客户会话负责人不满足 Agent 执行资格，周期已退回队列",
		"organization_id", session.OrganizationID,
		"conversation_id", session.ConversationID,
		"service_session_id", session.ID,
		"assignee_identity_id", assignee.ID,
		"cancelled_run_ids", cancelled,
	)
	return nil
}

// loadCustomerAssigneeType 读取当前客服负责人的企业身份类型。
func loadCustomerAssigneeType(ctx context.Context, db bun.IDB, session *servermodels.ServiceSession) (domain.OrganizationIdentityType, error) {
	if session.AssigneeIdentityID == nil {
		return "", nil
	}
	var identityType string
	if err := db.NewSelect().Model((*servermodels.OrganizationIdentity)(nil)).
		Column("type").
		Where("oi.organization_id = ?", session.OrganizationID).
		Where("oi.id = ?", *session.AssigneeIdentityID).
		Scan(ctx, &identityType); err != nil {
		return "", fmt.Errorf("load customer assignee identity type: %w", err)
	}
	return domain.OrganizationIdentityType(identityType), nil
}

// loadCustomerInputSender 校验来源消息属于当前周期且来自服务会话发起人，并返回其聊天主体。
func loadCustomerInputSender(ctx context.Context, db bun.IDB, session *servermodels.ServiceSession, messageID string) (string, error) {
	var subjectID string
	err := db.NewSelect().
		TableExpr("messages AS msg").
		ColumnExpr("cp.subject_id").
		Join("JOIN conversation_participants AS cp ON cp.id = msg.sender_participant_id AND cp.organization_id = msg.organization_id AND cp.conversation_id = msg.conversation_id").
		Join("JOIN service_conversations AS svc ON svc.organization_id = msg.organization_id AND svc.id = ? AND svc.requester_subject_id = cp.subject_id", session.ServiceConversationID).
		Where("msg.id = ?", messageID).
		Where("msg.organization_id = ?", session.OrganizationID).
		Where("msg.conversation_id = ?", session.ConversationID).
		Where("msg.service_session_id = ?", session.ID).
		Where("msg.type IN (?)", bun.In([]domain.MessageType{domain.MessageTypeText, domain.MessageTypeAttachment})).
		Where("msg.deleted_at IS NULL").
		Scan(ctx, &subjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load customer agent input sender: %w", err)
	}
	return subjectID, nil
}

// loadCustomerAgentEligibility 校验当前负责人及指定运行 Revision 可以执行服务周期：渠道来源要求 AI 员工服务客户且渠道支持 AI 接待，单聊要求是该会话服务员工的 AI 员工。
func loadCustomerAgentEligibility(ctx context.Context, db bun.IDB, session *servermodels.ServiceSession, runRevisionID string) (customerAgentEligibility, bool, error) {
	if session.AssigneeIdentityID == nil {
		return customerAgentEligibility{}, false, nil
	}
	var source string
	if err := db.NewSelect().Model((*servermodels.ServiceConversation)(nil)).Column("svc.source").
		Where("svc.organization_id = ? AND svc.id = ?", session.OrganizationID, session.ServiceConversationID).
		Scan(ctx, &source); err != nil {
		return customerAgentEligibility{}, false, fmt.Errorf("load service conversation source: %w", err)
	}
	row := customerAgentEligibility{}
	query := db.NewSelect().
		TableExpr("organization_identities AS oi").
		Join("JOIN agents AS a ON a.identity_id = oi.id AND a.organization_id = oi.organization_id").
		Where("oi.organization_id = ?", session.OrganizationID).
		Where("oi.id = ?", *session.AssigneeIdentityID).
		Where("oi.type = ?", domain.OrganizationIdentityTypeAgent)
	if domain.ServiceSource(source) == domain.ServiceSourceChannel {
		query = identityaction.ApplyServiceHandlingConditions(query).
			Join("JOIN channel_conversations AS cc ON cc.organization_id = oi.organization_id AND cc.conversation_id = ?", session.ConversationID).
			Join("JOIN contact_channel_identities AS cci ON cci.id = cc.contact_channel_identity_id AND cci.organization_id = cc.organization_id").
			Join("JOIN channels AS c ON c.id = cci.channel_id AND c.organization_id = cci.organization_id").
			Join("LEFT JOIN telegram_channel_settings AS tcs ON tcs.channel_id = c.id AND tcs.organization_id = c.organization_id").
			Where("c.type = ? OR (c.type = ? AND tcs.bot_id IS NOT NULL)", domain.ChannelTypeWebsite, domain.ChannelTypeTelegram)
	} else {
		query = identityaction.ApplyDirectServiceAgentConditions(query, session.ConversationID)
	}
	if runRevisionID == "" {
		// 服务条件已校验当前 Revision 可执行。
		query = query.ColumnExpr("a.active_revision_id AS revision_id")
	} else {
		query = query.
			ColumnExpr("? AS revision_id", runRevisionID).
			Join("JOIN agent_revisions AS ar ON ar.id = ? AND ar.agent_id = a.id AND ar.organization_id = a.organization_id AND ar.execution_mode = ? AND ar.schema_version = 1", runRevisionID, domain.AgentExecutionModeManaged)
	}
	err := query.Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return customerAgentEligibility{}, false, nil
	}
	if err != nil {
		return customerAgentEligibility{}, false, fmt.Errorf("load customer agent eligibility: %w", err)
	}
	return row, true, nil
}

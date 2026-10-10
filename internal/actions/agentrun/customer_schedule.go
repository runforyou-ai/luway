//go:build server

package agentrun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/servicehandoff"
	"github.com/runforyou-ai/luway/internal/actions/serviceroute"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/uptrace/bun"
)

// customerAgentEligibility 是负责 AI 员工可执行服务周期时使用的配置版本。
type customerAgentEligibility struct {
	RevisionID string `bun:"revision_id"`
}

// ScheduleCustomerAuto 把一条服务会话发起人的消息追加到当前负责 AI 员工的持久输入流。
func (s *Scheduler) ScheduleCustomerAuto(ctx context.Context, db bun.IDB, workspaceID, conversationID, serviceSessionID, messageID string) (bool, error) {
	if s.enqueuer == nil {
		return false, errors.New("agent run scheduler is unavailable")
	}
	locked, err := chatstate.LockServiceSession(ctx, db, workspaceID, conversationID)
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
	if assigneeType != domain.WorkspaceIdentityTypeAgent {
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
		WorkspaceID: workspaceID, ConversationID: conversationID,
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
	subject, err := chatstate.EnsureSubject(ctx, db, session.WorkspaceID, domain.ChatSubjectKindWorkspaceIdentity, *session.AssigneeIdentityID, uuid.NewV7().String())
	if err != nil {
		return false, err
	}
	if err := s.appendInput(ctx, db, agentRunSpec{
		WorkspaceID: session.WorkspaceID, ConversationID: session.ConversationID,
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
	assignee := &servermodels.WorkspaceIdentity{}
	if err := db.NewSelect().Model(assignee).
		Column("oi.id", "oi.type", "oi.display_name").
		Where("oi.workspace_id = ? AND oi.id = ?", session.WorkspaceID, *session.AssigneeIdentityID).
		Scan(ctx); err != nil {
		return fmt.Errorf("load unavailable customer agent identity: %w", err)
	}
	cancelled, err := servicehandoff.ReturnUnavailableAssigneeSession(ctx, db, s.enqueuer, session.WorkspaceID, session.ConversationID, session.ID, assignee, key)
	if err != nil {
		return err
	}
	slog.WarnContext(logscope.WithWorkspace(ctx, session.WorkspaceID), "客户会话负责人不满足 Agent 执行资格，周期已退回队列",
		"conversation_id", session.ConversationID,
		"service_session_id", session.ID,
		"assignee_identity_id", assignee.ID,
		"cancelled_run_ids", cancelled,
	)
	return nil
}

// loadCustomerAssigneeType 读取当前客服负责人的企业身份类型，调用方须确认周期已有负责人。
func loadCustomerAssigneeType(ctx context.Context, db bun.IDB, session *servermodels.ServiceSession) (domain.WorkspaceIdentityType, error) {
	var identityType string
	if err := db.NewSelect().Model((*servermodels.WorkspaceIdentity)(nil)).
		Column("type").
		Where("oi.workspace_id = ?", session.WorkspaceID).
		Where("oi.id = ?", *session.AssigneeIdentityID).
		Scan(ctx, &identityType); err != nil {
		return "", fmt.Errorf("load customer assignee identity type: %w", err)
	}
	return domain.WorkspaceIdentityType(identityType), nil
}

// loadCustomerInputSender 校验来源消息属于当前周期且来自服务会话发起人，并返回其聊天主体。
func loadCustomerInputSender(ctx context.Context, db bun.IDB, session *servermodels.ServiceSession, messageID string) (string, error) {
	var subjectID string
	err := db.NewSelect().
		TableExpr("messages AS msg").
		ColumnExpr("cp.subject_id").
		Join("JOIN conversation_participants AS cp ON cp.id = msg.sender_participant_id AND cp.workspace_id = msg.workspace_id AND cp.conversation_id = msg.conversation_id").
		Join("JOIN service_conversations AS svc ON svc.workspace_id = msg.workspace_id AND svc.id = ? AND svc.requester_subject_id = cp.subject_id", session.ServiceConversationID).
		Where("msg.id = ?", messageID).
		Where("msg.workspace_id = ?", session.WorkspaceID).
		Where("msg.conversation_id = ?", session.ConversationID).
		Where("msg.service_session_id = ?", session.ID).
		Where("msg.type IN (?)", bun.List([]domain.MessageType{domain.MessageTypeText, domain.MessageTypeAttachment})).
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

// loadCustomerAgentEligibility 校验当前负责人及指定运行 Revision 可以执行服务周期，调用方须确认周期已有负责人：渠道来源要求 AI 员工服务该会话的服务对象且渠道支持 AI 接待，单聊要求是该会话服务员工的 AI 员工。
func loadCustomerAgentEligibility(ctx context.Context, db bun.IDB, session *servermodels.ServiceSession, runRevisionID string) (customerAgentEligibility, bool, error) {
	var source, audience string
	if err := db.NewSelect().Model((*servermodels.ServiceConversation)(nil)).Column("svc.source", "svc.audience").
		Where("svc.workspace_id = ? AND svc.id = ?", session.WorkspaceID, session.ServiceConversationID).
		Scan(ctx, &source, &audience); err != nil {
		return customerAgentEligibility{}, false, fmt.Errorf("load service conversation source: %w", err)
	}
	row := customerAgentEligibility{}
	query := db.NewSelect().
		TableExpr("workspace_identities AS oi").
		Join("JOIN agents AS a ON a.identity_id = oi.id AND a.workspace_id = oi.workspace_id").
		Where("oi.workspace_id = ?", session.WorkspaceID).
		Where("oi.id = ?", *session.AssigneeIdentityID).
		Where("oi.type = ?", domain.WorkspaceIdentityTypeAgent)
	if domain.ServiceSource(source) == domain.ServiceSourceChannel {
		query = serviceroute.ApplyServiceHandlingConditions(query, []domain.ServiceAudience{domain.ServiceAudience(audience)}).
			Join("JOIN channel_conversations AS cc ON cc.workspace_id = oi.workspace_id AND cc.conversation_id = ?", session.ConversationID).
			Join("JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id AND ci.workspace_id = cc.workspace_id").
			Join("JOIN channels AS c ON c.id = ci.channel_id AND c.workspace_id = ci.workspace_id").
			Where("c.type = ? OR (c.type IN (?) AND c.provider_account_id IS NOT NULL)", domain.ChannelTypeWebsite, bun.List(domain.ChannelTypesWith(domain.ChannelCapabilities.ViaPlatform)))
	} else {
		query = serviceroute.ApplyDirectServiceAgentConditions(query, session.ConversationID)
	}
	if runRevisionID == "" {
		// 服务条件已校验当前 Revision 可执行。
		query = query.ColumnExpr("a.active_revision_id AS revision_id")
	} else {
		query = query.
			ColumnExpr("? AS revision_id", runRevisionID).
			Join("JOIN agent_revisions AS ar ON ar.id = ? AND ar.agent_id = a.id AND ar.workspace_id = a.workspace_id AND ar.execution_mode = ? AND ar.schema_version = 1", runRevisionID, domain.AgentExecutionModeManaged)
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

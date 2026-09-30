//go:build server

package servicesession

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/runforyou-ai/cervi/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	identityaction "github.com/runforyou-ai/cervi/internal/actions/identity"
	"github.com/runforyou-ai/cervi/internal/actions/serviceassignment"
	"github.com/runforyou-ai/cervi/internal/actions/servicesummary"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/realtime"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
	"github.com/runforyou-ai/cervi/internal/storage/server/pgerr"
	servertask "github.com/runforyou-ai/cervi/internal/task/server"
	"github.com/uptrace/bun"
)

// ServiceSessionAgentRunCoordinator 在客服事务内收敛原负责人的 Agent 执行。
type ServiceSessionAgentRunCoordinator interface {
	CancelForServiceSession(context.Context, bun.IDB, string, string, string, domain.AgentRunErrorCode) ([]string, error)
	CancelRunContexts([]string)
}

// ClaimServiceSessionAction 领取或接管服务会话当前处理周期。
type ClaimServiceSessionAction struct {
	db          *bun.DB
	coordinator ServiceSessionAgentRunCoordinator
	enqueuer    servertask.TxEnqueuer
}

// NewClaimServiceSessionAction 创建服务周期领取操作。
func NewClaimServiceSessionAction(db *bun.DB, coordinator ServiceSessionAgentRunCoordinator, enqueuer servertask.TxEnqueuer) *ClaimServiceSessionAction {
	return &ClaimServiceSessionAction{db: db, coordinator: coordinator, enqueuer: enqueuer}
}

// Execute 把未关闭处理周期负责人设置为当前身份，接管时为原负责人补分配。
func (a *ClaimServiceSessionAction) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string) (ServiceSessionResult, error) {
	conversationID, valid := common.NormalizeUUID(conversationID)
	if !valid {
		return ServiceSessionResult{}, &conversationaction.ValidationError{Fields: map[string]conversationaction.ValidationCode{"conversationId": conversationaction.ValidationConversationIDInvalid}}
	}
	var output ServiceSessionResult
	var cancelledRunIDs []string
	var cancelledSession *servermodels.ServiceSession
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := lockActiveServiceHandler(ctx, tx, identity); err != nil {
			return err
		}
		locked, err := lockOpenServiceSession(ctx, tx, identity.Organization.ID, conversationID)
		if err != nil {
			return err
		}
		conversation, service, session := locked.Conversation, locked.Service, locked.Session
		if err := rejectServiceRequester(ctx, tx, service, identity.OrganizationIdentity.ID); err != nil {
			return err
		}
		now := time.Now().UTC()
		if session.AssigneeIdentityID == nil || *session.AssigneeIdentityID != identity.OrganizationIdentity.ID {
			previousAssigneeID := session.AssigneeIdentityID
			if session.AssigneeIdentityID != nil {
				cancelledRunIDs, err = a.coordinator.CancelForServiceSession(
					ctx, tx, session.OrganizationID, session.ID,
					*session.AssigneeIdentityID, domain.AgentRunErrorCodeAssigneeChanged,
				)
				if err != nil {
					return err
				}
				cancelledSession = session
			}
			if err := chatstate.AssignServiceSession(ctx, tx, session, identity.OrganizationIdentity.ID, now); err != nil {
				return err
			}
			// 无人负责时记为领取，已有负责人时记为接管。
			eventType := domain.ConversationSystemEventServiceSessionClaimed
			if previousAssigneeID != nil {
				eventType = domain.ConversationSystemEventServiceSessionTakenOver
			}
			if err := appendServiceSessionEvent(ctx, tx, identity, conversation, session, domain.ServiceSource(service.Source), eventType, previousAssigneeID, nil); err != nil {
				return err
			}
			if previousAssigneeID != nil {
				if err := serviceassignment.EnqueueBackfill(ctx, tx, a.enqueuer, serviceassignment.BackfillInput{OrganizationID: session.OrganizationID, IdentityID: *previousAssigneeID}); err != nil {
					return err
				}
			}
			if err := chatstate.TouchConversation(ctx, tx, conversation, domain.ConversationChangeService); err != nil {
				return err
			}
		}
		output = serviceSessionResult(session, &identity.OrganizationIdentity)
		return nil
	})
	if err != nil {
		return ServiceSessionResult{}, fmt.Errorf("claim service session: %w", err)
	}
	finishServiceSessionAgentCancellation(a.coordinator, cancelledRunIDs, cancelledSession, domain.AgentRunErrorCodeAssigneeChanged)
	return output, nil
}

// TransferServiceSessionAction 把当前负责的处理周期转给成员、团队队列或公共队列。
type TransferServiceSessionAction struct {
	db          *bun.DB
	coordinator ServiceSessionAgentRunCoordinator
	scheduler   conversationaction.CustomerAgentMessageScheduler
	enqueuer    servertask.TxEnqueuer
}

// NewTransferServiceSessionAction 创建服务周期转交操作。
func NewTransferServiceSessionAction(db *bun.DB, coordinator ServiceSessionAgentRunCoordinator, scheduler conversationaction.CustomerAgentMessageScheduler, enqueuer servertask.TxEnqueuer) *TransferServiceSessionAction {
	return &TransferServiceSessionAction{db: db, coordinator: coordinator, scheduler: scheduler, enqueuer: enqueuer}
}

// Execute 校验当前负责人和转交去向后把处理周期交给成员、团队队列或公共队列，并为原负责人补分配；转给队列时排除原负责人重新分配本周期。
func (a *TransferServiceSessionAction) Execute(ctx context.Context, identity *servermodels.Identity, input TransferServiceSessionInput) (ServiceSessionResult, error) {
	input, err := normalizeTransferServiceSessionInput(identity, input)
	if err != nil {
		return ServiceSessionResult{}, err
	}
	var output ServiceSessionResult
	var cancelledRunIDs []string
	var cancelledSession *servermodels.ServiceSession
	err = realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := lockActiveServiceHandler(ctx, tx, identity); err != nil {
			return err
		}
		service, err := chatstate.LoadServiceConversation(ctx, tx, identity.Organization.ID, input.ConversationID)
		if err != nil {
			return err
		}
		if input.TargetKind == domain.ServiceSessionTargetMember {
			if err := rejectServiceRequester(ctx, tx, service, input.IdentityID); err != nil {
				return err
			}
		}
		// 按转交目标、会话的顺序取锁。
		target, targetIdentity, err := lockTransferTarget(ctx, tx, identity, service, input)
		if err != nil {
			return err
		}
		locked, err := lockOpenServiceSession(ctx, tx, identity.Organization.ID, input.ConversationID)
		if err != nil {
			return err
		}
		conversation, session := locked.Conversation, locked.Session
		if session.AssigneeIdentityID == nil || *session.AssigneeIdentityID != identity.OrganizationIdentity.ID {
			return &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonServiceSessionOwned}
		}
		if targetIdentity != nil && domain.OrganizationIdentityType(targetIdentity.Type) == domain.OrganizationIdentityTypeAgent && domain.ServiceSource(service.Source) == domain.ServiceSourceChannel {
			// 渠道会话确认来源渠道支持 AI 员工承接。
			var channelType domain.ChannelType
			if err := tx.NewSelect().TableExpr("channel_conversations AS cc").
				ColumnExpr("c.type").
				Join("JOIN contact_channel_identities AS cci ON cci.id = cc.contact_channel_identity_id AND cci.organization_id = cc.organization_id").
				Join("JOIN channels AS c ON c.id = cci.channel_id AND c.organization_id = cci.organization_id").
				Where("cc.conversation_id = ?", session.ConversationID).
				Where("cc.organization_id = ?", session.OrganizationID).
				Scan(ctx, &channelType); err != nil {
				return err
			}
			if !domain.ChannelSupportsAgentAssignee(channelType) {
				return &conversationaction.ValidationError{Fields: map[string]conversationaction.ValidationCode{"identityId": conversationaction.ValidationTargetIdentityIDInvalid}}
			}
		}
		cancelledRunIDs, err = a.coordinator.CancelForServiceSession(
			ctx, tx, session.OrganizationID, session.ID,
			*session.AssigneeIdentityID, domain.AgentRunErrorCodeAssigneeChanged,
		)
		if err != nil {
			return err
		}
		cancelledSession = session
		previousAssigneeID := *session.AssigneeIdentityID
		if err := applyTransferTarget(ctx, tx, identity, session, target, targetIdentity); err != nil {
			return err
		}
		if err := appendServiceSessionEvent(ctx, tx, identity, conversation, session, domain.ServiceSource(service.Source),
			domain.ConversationSystemEventServiceSessionTransferred, &previousAssigneeID, &target); err != nil {
			return err
		}
		// 原负责人腾出接待量后补分配，本周期转回队列时由分配任务排除原负责人另行分配。
		backfill := serviceassignment.BackfillInput{OrganizationID: session.OrganizationID, IdentityID: previousAssigneeID}
		if targetIdentity == nil {
			backfill.ExcludeServiceSessionID = session.ID
			if err := serviceassignment.EnqueueAssign(ctx, tx, a.enqueuer, serviceassignment.AssignInput{
				OrganizationID: session.OrganizationID, ServiceSessionID: session.ID, ExcludeIdentityID: previousAssigneeID,
			}); err != nil {
				return err
			}
		}
		if err := serviceassignment.EnqueueBackfill(ctx, tx, a.enqueuer, backfill); err != nil {
			return err
		}
		if err := chatstate.TouchConversation(ctx, tx, conversation, domain.ConversationChangeService); err != nil {
			return err
		}
		if targetIdentity != nil && domain.OrganizationIdentityType(targetIdentity.Type) == domain.OrganizationIdentityTypeAgent {
			messageID, fromRequester, err := loadServiceSessionLastMessageSender(ctx, tx, session)
			if err != nil {
				return err
			}
			if fromRequester {
				if a.scheduler == nil {
					return errors.New("customer agent scheduler is unavailable")
				}
				scheduled, err := a.scheduler.ScheduleCustomerAuto(
					ctx, tx, session.OrganizationID, session.ConversationID, session.ID, messageID,
				)
				if err != nil {
					return err
				}
				if !scheduled {
					return &conversationaction.ValidationError{Fields: map[string]conversationaction.ValidationCode{"identityId": conversationaction.ValidationTargetIdentityIDInvalid}}
				}
			}
		}
		output = serviceSessionResult(session, targetIdentity)
		return nil
	})
	if err != nil {
		return ServiceSessionResult{}, fmt.Errorf("transfer service session: %w", err)
	}
	finishServiceSessionAgentCancellation(a.coordinator, cancelledRunIDs, cancelledSession, domain.AgentRunErrorCodeAssigneeChanged)
	return output, nil
}

// normalizeTransferServiceSessionInput 规范化转交输入并校验各类去向所需的编号。
func normalizeTransferServiceSessionInput(identity *servermodels.Identity, input TransferServiceSessionInput) (TransferServiceSessionInput, error) {
	fields := map[string]conversationaction.ValidationCode{}
	conversationID, valid := common.NormalizeUUID(input.ConversationID)
	if !valid {
		fields["conversationId"] = conversationaction.ValidationConversationIDInvalid
	}
	input.ConversationID = conversationID
	switch input.TargetKind {
	case domain.ServiceSessionTargetMember:
		identityID, valid := common.NormalizeUUID(input.IdentityID)
		if !valid || identityID == identity.OrganizationIdentity.ID {
			fields["identityId"] = conversationaction.ValidationTargetIdentityIDInvalid
		}
		input.IdentityID = identityID
	case domain.ServiceSessionTargetTeam:
		teamID, valid := common.NormalizeUUID(input.TeamID)
		if !valid {
			fields["teamId"] = ValidationTargetTeamIDInvalid
		}
		input.TeamID = teamID
	case domain.ServiceSessionTargetPublicQueue:
	default:
		fields["kind"] = ValidationTransferTargetKindInvalid
	}
	if len(fields) > 0 {
		return input, &conversationaction.ValidationError{Fields: fields}
	}
	return input, nil
}

// lockTransferTarget 锁定转交去向并返回其名称快照；成员去向同时返回目标身份。企业成员发起的服务会话只能交给开启接待的真人成员或交还该会话的 AI 员工。
func lockTransferTarget(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, service *servermodels.ServiceConversation, input TransferServiceSessionInput) (domain.ServiceSessionTarget, *servermodels.OrganizationIdentity, error) {
	switch input.TargetKind {
	case domain.ServiceSessionTargetMember:
		lock := identityaction.LockActiveServiceHandlingIdentity
		if domain.ServiceSource(service.Source) != domain.ServiceSourceChannel {
			lock = func(ctx context.Context, db bun.IDB, organizationID, identityID string) (*servermodels.OrganizationIdentity, error) {
				return identityaction.LockServiceHandlingIdentity(ctx, db, organizationID, service.ConversationID, identityID)
			}
		}
		target, err := lock(ctx, tx, identity.Organization.ID, input.IdentityID)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ServiceSessionTarget{}, nil, &conversationaction.ValidationError{Fields: map[string]conversationaction.ValidationCode{"identityId": conversationaction.ValidationTargetIdentityIDInvalid}}
		}
		if err != nil {
			return domain.ServiceSessionTarget{}, nil, err
		}
		return domain.ServiceSessionTarget{Kind: domain.ServiceSessionTargetMember, IdentityID: &target.ID, DisplayName: &target.DisplayName}, target, nil
	case domain.ServiceSessionTargetTeam:
		team := &servermodels.Team{}
		err := tx.NewSelect().Model(team).Column("t.id", "t.name").
			Where("t.organization_id = ? AND t.id = ?", identity.Organization.ID, input.TeamID).
			For("KEY SHARE").
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ServiceSessionTarget{}, nil, &conversationaction.ValidationError{Fields: map[string]conversationaction.ValidationCode{"teamId": ValidationTargetTeamIDInvalid}}
		}
		if err != nil {
			return domain.ServiceSessionTarget{}, nil, err
		}
		available, err := identityaction.TeamHasServiceHandler(ctx, tx, identity.Organization.ID, team.ID)
		if err != nil {
			return domain.ServiceSessionTarget{}, nil, err
		}
		if !available {
			return domain.ServiceSessionTarget{}, nil, &conversationaction.ConflictError{Reason: ConflictReasonTransferTeamUnavailable}
		}
		return domain.ServiceSessionTarget{Kind: domain.ServiceSessionTargetTeam, TeamID: &team.ID, TeamName: &team.Name}, nil, nil
	default:
		return domain.ServiceSessionTarget{Kind: domain.ServiceSessionTargetPublicQueue}, nil, nil
	}
}

// applyTransferTarget 按转交去向写入负责人与所属队列；转给团队或公共队列时清空负责人并从此刻计入队列，转给成员时保持原队列；周期首次转给 AI 员工时记为其接待。
func applyTransferTarget(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, session *servermodels.ServiceSession, target domain.ServiceSessionTarget, targetIdentity *servermodels.OrganizationIdentity) error {
	now := time.Now().UTC()
	if target.Kind != domain.ServiceSessionTargetMember {
		return chatstate.ReturnServiceSessionToQueue(ctx, tx, session, target.TeamID, now)
	}
	if err := chatstate.AssignServiceSession(ctx, tx, session, targetIdentity.ID, now); err != nil {
		return err
	}
	// 周期首次转给 AI 员工时记为其接待。
	if domain.OrganizationIdentityType(targetIdentity.Type) == domain.OrganizationIdentityTypeAgent && session.AgentIdentityID == nil {
		if _, err := tx.NewUpdate().Model(session).Set("agent_identity_id = ?", targetIdentity.ID).
			WherePK().Where("organization_id = ?", identity.Organization.ID).Exec(ctx); err != nil {
			return err
		}
		session.AgentIdentityID = &targetIdentity.ID
	}
	return nil
}

// loadServiceSessionLastMessageSender 读取当前处理周期最后一条共享消息及其是否由发起人发送。
func loadServiceSessionLastMessageSender(ctx context.Context, db bun.IDB, session *servermodels.ServiceSession) (string, bool, error) {
	row := struct {
		MessageID     string `bun:"message_id"`
		FromRequester bool   `bun:"from_requester"`
	}{}
	err := db.NewSelect().
		TableExpr("messages AS msg").
		ColumnExpr("msg.id AS message_id, cp.subject_id = svc.requester_subject_id AS from_requester").
		Join("JOIN conversation_participants AS cp ON cp.id = msg.sender_participant_id AND cp.organization_id = msg.organization_id AND cp.conversation_id = msg.conversation_id").
		Join("JOIN service_conversations AS svc ON svc.organization_id = msg.organization_id AND svc.id = ?", session.ServiceConversationID).
		Where("msg.organization_id = ?", session.OrganizationID).
		Where("msg.conversation_id = ?", session.ConversationID).
		Where("msg.service_session_id = ?", session.ID).
		Where("msg.id = ?", session.LastMessageID).
		Where("msg.deleted_at IS NULL").
		Scan(ctx, &row)
	if err != nil {
		return "", false, fmt.Errorf("load service session last message sender: %w", err)
	}
	return row.MessageID, row.FromRequester, nil
}

// CloseServiceSessionAction 关闭服务会话当前处理周期。
type CloseServiceSessionAction struct {
	db          *bun.DB
	coordinator ServiceSessionAgentRunCoordinator
	enqueuer    servertask.TxEnqueuer
}

// NewCloseServiceSessionAction 创建服务周期关闭操作。
func NewCloseServiceSessionAction(db *bun.DB, coordinator ServiceSessionAgentRunCoordinator, enqueuer servertask.TxEnqueuer) *CloseServiceSessionAction {
	return &CloseServiceSessionAction{db: db, coordinator: coordinator, enqueuer: enqueuer}
}

// Execute 关闭公共队列或当前身份负责的处理周期，无人负责的周期由关闭人成为负责人；关闭本人负责的周期后为本人补分配。
func (a *CloseServiceSessionAction) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string) (ServiceSessionResult, error) {
	conversationID, valid := common.NormalizeUUID(conversationID)
	if !valid {
		return ServiceSessionResult{}, &conversationaction.ValidationError{Fields: map[string]conversationaction.ValidationCode{"conversationId": conversationaction.ValidationConversationIDInvalid}}
	}
	var output ServiceSessionResult
	var cancelledRunIDs []string
	var cancelledSession *servermodels.ServiceSession
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := lockActiveServiceHandler(ctx, tx, identity); err != nil {
			return err
		}
		locked, err := lockOpenServiceSession(ctx, tx, identity.Organization.ID, conversationID)
		if err != nil {
			return err
		}
		conversation, service, session := locked.Conversation, locked.Service, locked.Session
		if session.AssigneeIdentityID != nil && *session.AssigneeIdentityID != identity.OrganizationIdentity.ID {
			return &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonServiceSessionOwned}
		}
		if err := rejectServiceRequester(ctx, tx, service, identity.OrganizationIdentity.ID); err != nil {
			return err
		}
		if session.AssigneeIdentityID != nil {
			cancelledRunIDs, err = a.coordinator.CancelForServiceSession(
				ctx, tx, session.OrganizationID, session.ID,
				*session.AssigneeIdentityID, domain.AgentRunErrorCodeSessionClosed,
			)
			if err != nil {
				return err
			}
			cancelledSession = session
		}
		now := time.Now().UTC()
		assigneeIdentityID := identity.OrganizationIdentity.ID
		ownedBefore := session.AssigneeIdentityID != nil
		// 无人负责的周期由关闭人领取后关闭。
		if !ownedBefore {
			if err := chatstate.AssignServiceSession(ctx, tx, session, assigneeIdentityID, now); err != nil {
				return err
			}
		}
		if err := chatstate.CloseServiceSession(ctx, tx, session, assigneeIdentityID, domain.ServiceSessionCloseManual, now); err != nil {
			return err
		}
		if err := appendServiceSessionClosedEvent(ctx, tx, conversation, session, domain.ServiceSource(service.Source), identity.OrganizationIdentity.ID, identity.OrganizationIdentity.DisplayName, domain.ServiceSessionCloseManual); err != nil {
			return err
		}
		if err := servicesummary.MarkClosed(ctx, tx, a.enqueuer, session, domain.ServiceSessionCloseManual); err != nil {
			return err
		}
		if ownedBefore {
			if err := serviceassignment.EnqueueBackfill(ctx, tx, a.enqueuer, serviceassignment.BackfillInput{OrganizationID: session.OrganizationID, IdentityID: assigneeIdentityID}); err != nil {
				return err
			}
		}
		if err := chatstate.TouchConversation(ctx, tx, conversation, domain.ConversationChangeService); err != nil {
			return err
		}
		// 读取处理周期负责人身份。
		assignee := &servermodels.OrganizationIdentity{}
		if err := tx.NewSelect().Model(assignee).
			Column("oi.id", "oi.type", "oi.display_name", "oi.avatar_file_id").
			Where("oi.organization_id = ?", identity.Organization.ID).
			Where("oi.id = ?", assigneeIdentityID).
			Scan(ctx); err != nil {
			return err
		}
		output = serviceSessionResult(session, assignee)
		return nil
	})
	if err != nil {
		return ServiceSessionResult{}, fmt.Errorf("close service session: %w", err)
	}
	finishServiceSessionAgentCancellation(a.coordinator, cancelledRunIDs, cancelledSession, domain.AgentRunErrorCodeSessionClosed)
	return output, nil
}

// CloseAgentServiceSession 在调用方持有会话锁的事务中关闭 AI 员工负责的开放周期：写入结束方式与关闭事件并准备小结，关闭人为负责的 AI 员工。
func CloseAgentServiceSession(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, conversation *servermodels.Conversation, session *servermodels.ServiceSession, source domain.ServiceSource, reason domain.ServiceSessionCloseReason) error {
	if domain.ServiceSessionStatus(session.Status) != domain.ServiceSessionStatusOpen || session.AssigneeIdentityID == nil {
		return conversationaction.ErrDataInvariant
	}
	agent := &servermodels.OrganizationIdentity{}
	if err := db.NewSelect().Model(agent).Column("oi.id", "oi.display_name").
		Where("oi.organization_id = ? AND oi.id = ? AND oi.type = ?", session.OrganizationID, *session.AssigneeIdentityID, domain.OrganizationIdentityTypeAgent).
		Scan(ctx); err != nil {
		return fmt.Errorf("load closing agent identity: %w", err)
	}
	if err := chatstate.CloseServiceSession(ctx, db, session, agent.ID, reason, time.Now().UTC()); err != nil {
		return err
	}
	if err := appendServiceSessionClosedEvent(ctx, db, conversation, session, source, agent.ID, agent.DisplayName, reason); err != nil {
		return err
	}
	if err := servicesummary.MarkClosed(ctx, db, enqueuer, session, reason); err != nil {
		return err
	}
	return chatstate.TouchConversation(ctx, db, conversation, domain.ConversationChangeService)
}

// ReopenServiceSessionAction 重新打开已关闭的服务会话处理周期。
type ReopenServiceSessionAction struct{ db *bun.DB }

// NewReopenServiceSessionAction 创建服务周期重新打开操作。
func NewReopenServiceSessionAction(db *bun.DB) *ReopenServiceSessionAction {
	return &ReopenServiceSessionAction{db: db}
}

// Execute 重新打开当前处理周期并分配给当前身份。
func (a *ReopenServiceSessionAction) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string) (ServiceSessionResult, error) {
	conversationID, valid := common.NormalizeUUID(conversationID)
	if !valid {
		return ServiceSessionResult{}, &conversationaction.ValidationError{Fields: map[string]conversationaction.ValidationCode{"conversationId": conversationaction.ValidationConversationIDInvalid}}
	}
	var output ServiceSessionResult
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := lockActiveServiceHandler(ctx, tx, identity); err != nil {
			return err
		}
		locked, err := chatstate.LockServiceSession(ctx, tx, identity.Organization.ID, conversationID)
		if err != nil {
			return err
		}
		conversation, service, session := locked.Conversation, locked.Service, locked.Session
		if err := rejectServiceRequester(ctx, tx, service, identity.OrganizationIdentity.ID); err != nil {
			return err
		}
		if domain.ServiceSessionStatus(session.Status) == domain.ServiceSessionStatusOpen {
			return &conversationaction.ConflictError{Reason: ConflictReasonServiceSessionAlreadyOpen}
		}
		if domain.ServiceSessionStatus(session.Status) != domain.ServiceSessionStatusClosed {
			return conversationaction.ErrDataInvariant
		}
		if err := chatstate.ReopenServiceSession(ctx, tx, session, identity.OrganizationIdentity.ID, time.Now().UTC()); err != nil {
			return err
		}
		if err := appendServiceSessionEvent(ctx, tx, identity, conversation, session, domain.ServiceSource(service.Source), domain.ConversationSystemEventServiceSessionReopened, nil, nil); err != nil {
			return err
		}
		if err := servicesummary.MarkReopened(ctx, tx, session); err != nil {
			return err
		}
		if err := chatstate.TouchConversation(ctx, tx, conversation, domain.ConversationChangeService); err != nil {
			return err
		}
		output = serviceSessionResult(session, &identity.OrganizationIdentity)
		return nil
	})
	if err != nil {
		if pgerr.UniqueViolationOn(err, "service_sessions_organization_service_conversation_open_unique") {
			err = &conversationaction.ConflictError{Reason: ConflictReasonServiceSessionAlreadyOpen}
		}
		return ServiceSessionResult{}, fmt.Errorf("reopen service session: %w", err)
	}
	return output, nil
}

// lockActiveServiceHandler 锁定当前身份的有效账号并校验其已开启处理服务请求。
func lockActiveServiceHandler(ctx context.Context, tx bun.Tx, identity *servermodels.Identity) error {
	err := identityaction.LockActiveServiceHandlingUser(ctx, tx, identity)
	if errors.Is(err, identityaction.ErrServiceHandlingRequired) {
		return &conversationaction.ConflictError{Reason: ConflictReasonServiceHandlingRequired}
	}
	return err
}

// lockOpenServiceSession 锁定服务会话及其最新且未关闭的服务周期。
func lockOpenServiceSession(ctx context.Context, db bun.IDB, organizationID, conversationID string) (chatstate.LockedServiceSession, error) {
	locked, err := chatstate.LockServiceSession(ctx, db, organizationID, conversationID)
	if err != nil {
		return chatstate.LockedServiceSession{}, err
	}
	if domain.ServiceSessionStatus(locked.Session.Status) != domain.ServiceSessionStatusOpen {
		return chatstate.LockedServiceSession{}, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonServiceSessionNotReplyable}
	}
	return locked, nil
}

// serviceSessionResult 转换服务周期命令结果。
func serviceSessionResult(session *servermodels.ServiceSession, assignee *servermodels.OrganizationIdentity) ServiceSessionResult {
	var resultAssignee *ServiceSessionAssignee
	if assignee != nil {
		resultAssignee = &ServiceSessionAssignee{IdentityID: assignee.ID, Type: domain.OrganizationIdentityType(assignee.Type), DisplayName: assignee.DisplayName, AvatarFileID: assignee.AvatarFileID}
	}
	return ServiceSessionResult{ID: session.ID, Status: domain.ServiceSessionStatus(session.Status), Assignee: resultAssignee, ClosedAt: session.ClosedAt}
}

// finishServiceSessionAgentCancellation 在事务提交后取消本进程中的模型调用并记录结果。
func finishServiceSessionAgentCancellation(coordinator ServiceSessionAgentRunCoordinator, runIDs []string, session *servermodels.ServiceSession, reason domain.AgentRunErrorCode) {
	if len(runIDs) == 0 {
		return
	}
	coordinator.CancelRunContexts(runIDs)
	for _, runID := range runIDs {
		slog.Info("已取消客服会话 Agent 运行",
			"agent_run_id", runID,
			"service_session_id", session.ID,
			"conversation_id", session.ConversationID,
			"reason", reason,
		)
	}
}

//go:build server

package servicesession

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/actions/serviceassignment"
	"github.com/runforyou-ai/luway/internal/actions/serviceroute"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/realtime"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/internal/storage/server/pgerr"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// ServiceSessionAgentRunCoordinator 在客服事务内收敛原负责人的 Agent 执行。
type ServiceSessionAgentRunCoordinator interface {
	CancelForServiceSession(context.Context, bun.IDB, string, string, string, domain.AgentRunErrorCode) ([]string, error)
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
	conversationID, _ = str.NormalizeUUID(conversationID)
	var output ServiceSessionResult
	var cancelledRunIDs []string
	var cancelledSession *servermodels.ServiceSession
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := lockActiveServiceHandler(ctx, tx, identity); err != nil {
			return err
		}
		locked, err := lockOpenServiceSession(ctx, tx, identity.Workspace.ID, conversationID)
		if err != nil {
			return err
		}
		conversation, service, session := locked.Conversation, locked.Service, locked.Session
		if err := rejectServiceRequester(ctx, tx, service, identity.WorkspaceIdentity.ID); err != nil {
			return err
		}
		// 写入时刻取持有会话锁之后的数据库时刻。
		now, err := serverstorage.ClockNow(ctx, tx)
		if err != nil {
			return err
		}
		if session.AssigneeIdentityID == nil || *session.AssigneeIdentityID != identity.WorkspaceIdentity.ID {
			previousAssigneeID := session.AssigneeIdentityID
			if session.AssigneeIdentityID != nil {
				cancelledRunIDs, err = a.coordinator.CancelForServiceSession(
					ctx, tx, session.WorkspaceID, session.ID,
					*session.AssigneeIdentityID, domain.AgentRunErrorCodeAssigneeChanged,
				)
				if err != nil {
					return err
				}
				cancelledSession = session
			}
			if err := servicestate.Begin(session).Assign(identity.WorkspaceIdentity.ID, now).Save(ctx, tx, a.enqueuer); err != nil {
				return err
			}
			// 无人负责时记为领取，已有负责人时记为接管。
			eventType := domain.ConversationSystemEventServiceSessionClaimed
			if previousAssigneeID != nil {
				eventType = domain.ConversationSystemEventServiceSessionTakenOver
			}
			if err := appendServiceSessionEvent(ctx, tx, a.enqueuer, identity, conversation, session, domain.ServiceSource(service.Source), eventType, previousAssigneeID, nil); err != nil {
				return err
			}
			if previousAssigneeID != nil {
				if err := serviceassignment.EnqueueBackfill(ctx, tx, a.enqueuer, serviceassignment.BackfillInput{WorkspaceID: session.WorkspaceID, IdentityID: *previousAssigneeID}); err != nil {
					return err
				}
			}
			if err := chatstate.TouchConversation(ctx, tx, conversation, domain.ConversationChangeService); err != nil {
				return err
			}
		}
		output = serviceSessionResult(session, &identity.WorkspaceIdentity)
		return nil
	})
	if err != nil {
		return ServiceSessionResult{}, fmt.Errorf("claim service session: %w", err)
	}
	logServiceSessionAgentCancellation(ctx, cancelledRunIDs, cancelledSession, domain.AgentRunErrorCodeAssigneeChanged)
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
		service, err := chatstate.LoadServiceConversation(ctx, tx, identity.Workspace.ID, input.ConversationID)
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
		locked, err := lockOpenServiceSession(ctx, tx, identity.Workspace.ID, input.ConversationID)
		if err != nil {
			return err
		}
		conversation, session := locked.Conversation, locked.Session
		if session.AssigneeIdentityID == nil || *session.AssigneeIdentityID != identity.WorkspaceIdentity.ID {
			return &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonServiceSessionOwned}
		}
		if targetIdentity != nil && domain.WorkspaceIdentityType(targetIdentity.Type) == domain.WorkspaceIdentityTypeAgent && domain.ServiceSource(service.Source) == domain.ServiceSourceChannel {
			// 渠道会话确认来源渠道支持 AI 员工承接。
			var channelType domain.ChannelType
			if err := tx.NewSelect().TableExpr("channel_conversations AS cc").
				ColumnExpr("c.type").
				Join("JOIN channel_identities AS ci ON ci.id = cc.channel_identity_id AND ci.workspace_id = cc.workspace_id").
				Join("JOIN channels AS c ON c.id = ci.channel_id AND c.workspace_id = ci.workspace_id").
				Where("cc.conversation_id = ?", session.ConversationID).
				Where("cc.workspace_id = ?", session.WorkspaceID).
				Scan(ctx, &channelType); err != nil {
				return err
			}
			if !domain.ChannelCapabilitiesOf(channelType).Outbound() {
				return &conversationaction.ValidationError{Fields: map[string]conversationaction.ValidationCode{"identityId": conversationaction.ValidationTargetIdentityIDInvalid}}
			}
		}
		cancelledRunIDs, err = a.coordinator.CancelForServiceSession(
			ctx, tx, session.WorkspaceID, session.ID,
			*session.AssigneeIdentityID, domain.AgentRunErrorCodeAssigneeChanged,
		)
		if err != nil {
			return err
		}
		cancelledSession = session
		previousAssigneeID := *session.AssigneeIdentityID
		if err := applyTransferTarget(ctx, tx, a.enqueuer, session, target, targetIdentity); err != nil {
			return err
		}
		if err := appendServiceSessionEvent(ctx, tx, a.enqueuer, identity, conversation, session, domain.ServiceSource(service.Source),
			domain.ConversationSystemEventServiceSessionTransferred, &previousAssigneeID, &target); err != nil {
			return err
		}
		// 原负责人腾出接待量后补分配，本周期转回队列时由分配任务排除原负责人另行分配。
		backfill := serviceassignment.BackfillInput{WorkspaceID: session.WorkspaceID, IdentityID: previousAssigneeID}
		if targetIdentity == nil {
			backfill.ExcludeServiceSessionID = session.ID
			if err := serviceassignment.EnqueueAssign(ctx, tx, a.enqueuer, serviceassignment.AssignInput{
				WorkspaceID: session.WorkspaceID, ServiceSessionID: session.ID, ExcludeIdentityID: previousAssigneeID,
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
		if targetIdentity != nil && domain.WorkspaceIdentityType(targetIdentity.Type) == domain.WorkspaceIdentityTypeAgent {
			messageID, fromRequester, err := loadServiceSessionLastMessageSender(ctx, tx, session)
			if err != nil {
				return err
			}
			if fromRequester {
				scheduled, err := a.scheduler.ScheduleCustomerAuto(
					ctx, tx, session.WorkspaceID, session.ConversationID, session.ID, messageID,
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
	logServiceSessionAgentCancellation(ctx, cancelledRunIDs, cancelledSession, domain.AgentRunErrorCodeAssigneeChanged)
	return output, nil
}

// normalizeTransferServiceSessionInput 规范化转交输入并校验各类去向所需的编号。
func normalizeTransferServiceSessionInput(identity *servermodels.Identity, input TransferServiceSessionInput) (TransferServiceSessionInput, error) {
	fields := map[string]conversationaction.ValidationCode{}
	input.ConversationID, _ = str.NormalizeUUID(input.ConversationID)
	switch input.TargetKind {
	case domain.ServiceSessionTargetMember:
		identityID, valid := str.NormalizeUUID(input.IdentityID)
		if !valid || identityID == identity.WorkspaceIdentity.ID {
			fields["identityId"] = conversationaction.ValidationTargetIdentityIDInvalid
		}
		input.IdentityID = identityID
	case domain.ServiceSessionTargetTeam:
		teamID, valid := str.NormalizeUUID(input.TeamID)
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
func lockTransferTarget(ctx context.Context, tx bun.Tx, identity *servermodels.Identity, service *servermodels.ServiceConversation, input TransferServiceSessionInput) (domain.ServiceSessionTarget, *servermodels.WorkspaceIdentity, error) {
	switch input.TargetKind {
	case domain.ServiceSessionTargetMember:
		lock := func(ctx context.Context, db bun.IDB, workspaceID, identityID string) (*servermodels.WorkspaceIdentity, error) {
			return serviceroute.LockActiveServiceHandlingIdentity(ctx, db, workspaceID, identityID, domain.ServiceAudience(service.Audience))
		}
		if domain.ServiceSource(service.Source) != domain.ServiceSourceChannel {
			lock = func(ctx context.Context, db bun.IDB, workspaceID, identityID string) (*servermodels.WorkspaceIdentity, error) {
				return serviceroute.LockServiceHandlingIdentity(ctx, db, workspaceID, service.ConversationID, identityID)
			}
		}
		target, err := lock(ctx, tx, identity.Workspace.ID, input.IdentityID)
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
			Where("t.workspace_id = ? AND t.id = ?", identity.Workspace.ID, input.TeamID).
			For("KEY SHARE").
			Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ServiceSessionTarget{}, nil, &conversationaction.ValidationError{Fields: map[string]conversationaction.ValidationCode{"teamId": ValidationTargetTeamIDInvalid}}
		}
		if err != nil {
			return domain.ServiceSessionTarget{}, nil, err
		}
		available, err := serviceroute.TeamHasServiceHandler(ctx, tx, identity.Workspace.ID, team.ID)
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

// applyTransferTarget 按转交去向写入负责人与所属队列；转给团队或公共队列时清空负责人并从此刻计入队列，转给成员时保持原队列；转给 AI 员工时保留真人接手记录，周期首次转给 AI 员工时记为其接待。
func applyTransferTarget(ctx context.Context, tx bun.Tx, enqueuer servertask.TxEnqueuer, session *servermodels.ServiceSession, target domain.ServiceSessionTarget, targetIdentity *servermodels.WorkspaceIdentity) error {
	// 写入时刻取持有会话锁之后的数据库时刻。
	now, err := serverstorage.ClockNow(ctx, tx)
	if err != nil {
		return err
	}
	transition := servicestate.Begin(session)
	switch {
	case target.Kind != domain.ServiceSessionTargetMember:
		transition.Queue(target.TeamID, now)
	case domain.WorkspaceIdentityType(targetIdentity.Type) == domain.WorkspaceIdentityTypeAgent:
		transition.AssignAgent(targetIdentity.ID, now)
	default:
		transition.Assign(targetIdentity.ID, now)
	}
	return transition.Save(ctx, tx, enqueuer)
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
		Join("JOIN conversation_participants AS cp ON cp.id = msg.sender_participant_id AND cp.workspace_id = msg.workspace_id AND cp.conversation_id = msg.conversation_id").
		Join("JOIN service_conversations AS svc ON svc.workspace_id = msg.workspace_id AND svc.id = ?", session.ServiceConversationID).
		Where("msg.workspace_id = ?", session.WorkspaceID).
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
	conversationID, _ = str.NormalizeUUID(conversationID)
	var output ServiceSessionResult
	var cancelledRunIDs []string
	var cancelledSession *servermodels.ServiceSession
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := lockActiveServiceHandler(ctx, tx, identity); err != nil {
			return err
		}
		locked, err := lockOpenServiceSession(ctx, tx, identity.Workspace.ID, conversationID)
		if err != nil {
			return err
		}
		conversation, service, session := locked.Conversation, locked.Service, locked.Session
		if session.AssigneeIdentityID != nil && *session.AssigneeIdentityID != identity.WorkspaceIdentity.ID {
			return &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonServiceSessionOwned}
		}
		if err := rejectServiceRequester(ctx, tx, service, identity.WorkspaceIdentity.ID); err != nil {
			return err
		}
		if session.AssigneeIdentityID != nil {
			cancelledRunIDs, err = a.coordinator.CancelForServiceSession(
				ctx, tx, session.WorkspaceID, session.ID,
				*session.AssigneeIdentityID, domain.AgentRunErrorCodeSessionClosed,
			)
			if err != nil {
				return err
			}
			cancelledSession = session
		}
		// 写入时刻取持有会话锁之后的数据库时刻。
		now, err := serverstorage.ClockNow(ctx, tx)
		if err != nil {
			return err
		}
		assigneeIdentityID := identity.WorkspaceIdentity.ID
		ownedBefore := session.AssigneeIdentityID != nil
		// 无人负责的周期由关闭人领取后关闭。
		transition := servicestate.Begin(session)
		if !ownedBefore {
			transition.Assign(assigneeIdentityID, now)
		}
		if err := transition.Close(assigneeIdentityID, domain.ServiceSessionCloseManual, now).Save(ctx, tx, a.enqueuer); err != nil {
			return err
		}
		if err := appendServiceSessionClosedEvent(ctx, tx, a.enqueuer, conversation, session, domain.ServiceSource(service.Source), identity.WorkspaceIdentity.ID, identity.WorkspaceIdentity.DisplayName, domain.ServiceSessionCloseManual); err != nil {
			return err
		}
		if err := servicesummary.MarkClosed(ctx, tx, a.enqueuer, session, domain.ServiceSessionCloseManual); err != nil {
			return err
		}
		if ownedBefore {
			if err := serviceassignment.EnqueueBackfill(ctx, tx, a.enqueuer, serviceassignment.BackfillInput{WorkspaceID: session.WorkspaceID, IdentityID: assigneeIdentityID}); err != nil {
				return err
			}
		}
		if err := chatstate.TouchConversation(ctx, tx, conversation, domain.ConversationChangeService); err != nil {
			return err
		}
		// 读取处理周期负责人身份。
		assignee := &servermodels.WorkspaceIdentity{}
		if err := tx.NewSelect().Model(assignee).
			Column("oi.id", "oi.type", "oi.display_name", "oi.avatar_file_id").
			Where("oi.workspace_id = ?", identity.Workspace.ID).
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
	logServiceSessionAgentCancellation(ctx, cancelledRunIDs, cancelledSession, domain.AgentRunErrorCodeSessionClosed)
	return output, nil
}

// CloseAgentServiceSession 在调用方持有会话锁的事务中关闭 AI 员工负责的开放周期：写入结束方式与关闭事件并准备小结，关闭人为负责的 AI 员工。
func CloseAgentServiceSession(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, conversation *servermodels.Conversation, session *servermodels.ServiceSession, source domain.ServiceSource, reason domain.ServiceSessionCloseReason) error {
	if domain.ServiceSessionStatus(session.Status) != domain.ServiceSessionStatusOpen || session.AssigneeIdentityID == nil {
		return conversationaction.ErrDataInvariant
	}
	agent := &servermodels.WorkspaceIdentity{}
	if err := db.NewSelect().Model(agent).Column("oi.id", "oi.display_name").
		Where("oi.workspace_id = ? AND oi.id = ? AND oi.type = ?", session.WorkspaceID, *session.AssigneeIdentityID, domain.WorkspaceIdentityTypeAgent).
		Scan(ctx); err != nil {
		return fmt.Errorf("load closing agent identity: %w", err)
	}
	// 写入时刻取持有会话锁之后的数据库时刻。
	now, err := serverstorage.ClockNow(ctx, db)
	if err != nil {
		return err
	}
	if err := servicestate.Begin(session).Close(agent.ID, reason, now).Save(ctx, db, enqueuer); err != nil {
		return err
	}
	if err := appendServiceSessionClosedEvent(ctx, db, enqueuer, conversation, session, source, agent.ID, agent.DisplayName, reason); err != nil {
		return err
	}
	if err := servicesummary.MarkClosed(ctx, db, enqueuer, session, reason); err != nil {
		return err
	}
	return chatstate.TouchConversation(ctx, db, conversation, domain.ConversationChangeService)
}

// ReopenServiceSessionAction 重新打开已关闭的服务会话处理周期。
type ReopenServiceSessionAction struct {
	db       *bun.DB
	enqueuer servertask.TxEnqueuer
}

// NewReopenServiceSessionAction 创建服务周期重新打开操作。
func NewReopenServiceSessionAction(db *bun.DB, enqueuer servertask.TxEnqueuer) *ReopenServiceSessionAction {
	return &ReopenServiceSessionAction{db: db, enqueuer: enqueuer}
}

// Execute 重新打开当前处理周期并分配给当前身份。
func (a *ReopenServiceSessionAction) Execute(ctx context.Context, identity *servermodels.Identity, conversationID string) (ServiceSessionResult, error) {
	conversationID, _ = str.NormalizeUUID(conversationID)
	var output ServiceSessionResult
	err := realtime.RunInTx(ctx, a.db, func(ctx context.Context, tx bun.Tx) error {
		if err := lockActiveServiceHandler(ctx, tx, identity); err != nil {
			return err
		}
		locked, err := chatstate.LockServiceSession(ctx, tx, identity.Workspace.ID, conversationID)
		if err != nil {
			return err
		}
		conversation, service, session := locked.Conversation, locked.Service, locked.Session
		if err := rejectServiceRequester(ctx, tx, service, identity.WorkspaceIdentity.ID); err != nil {
			return err
		}
		if domain.ServiceSessionStatus(session.Status) == domain.ServiceSessionStatusOpen {
			return &conversationaction.ConflictError{Reason: ConflictReasonServiceSessionAlreadyOpen}
		}
		// 写入时刻取持有会话锁之后的数据库时刻。
		now, err := serverstorage.ClockNow(ctx, tx)
		if err != nil {
			return err
		}
		if err := servicestate.Begin(session).Reopen(now).Assign(identity.WorkspaceIdentity.ID, now).Save(ctx, tx, a.enqueuer); err != nil {
			return err
		}
		if err := appendServiceSessionEvent(ctx, tx, a.enqueuer, identity, conversation, session, domain.ServiceSource(service.Source), domain.ConversationSystemEventServiceSessionReopened, nil, nil); err != nil {
			return err
		}
		if err := servicesummary.MarkReopened(ctx, tx, session); err != nil {
			return err
		}
		if err := chatstate.TouchConversation(ctx, tx, conversation, domain.ConversationChangeService); err != nil {
			return err
		}
		output = serviceSessionResult(session, &identity.WorkspaceIdentity)
		return nil
	})
	if err != nil {
		if pgerr.UniqueViolationOn(err, serverstorage.UniqueServiceSessionOpen) {
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
func lockOpenServiceSession(ctx context.Context, db bun.IDB, workspaceID, conversationID string) (chatstate.LockedServiceSession, error) {
	locked, err := chatstate.LockServiceSession(ctx, db, workspaceID, conversationID)
	if err != nil {
		return chatstate.LockedServiceSession{}, err
	}
	if domain.ServiceSessionStatus(locked.Session.Status) != domain.ServiceSessionStatusOpen {
		return chatstate.LockedServiceSession{}, &conversationaction.ConflictError{Reason: conversationaction.ConflictReasonServiceSessionNotReplyable}
	}
	return locked, nil
}

// serviceSessionResult 转换服务周期命令结果。
func serviceSessionResult(session *servermodels.ServiceSession, assignee *servermodels.WorkspaceIdentity) ServiceSessionResult {
	resultAssignee := support.MapPtr(assignee, func(assignee servermodels.WorkspaceIdentity) ServiceSessionAssignee {
		return ServiceSessionAssignee{IdentityID: assignee.ID, Type: domain.WorkspaceIdentityType(assignee.Type), DisplayName: assignee.DisplayName, AvatarFileID: assignee.AvatarFileID}
	})
	return ServiceSessionResult{ID: session.ID, Status: domain.ServiceSessionStatus(session.Status), Assignee: resultAssignee, ClosedAt: session.ClosedAt}
}

// logServiceSessionAgentCancellation 在事务提交后记录被取消的客服会话 Agent 运行。
func logServiceSessionAgentCancellation(ctx context.Context, runIDs []string, session *servermodels.ServiceSession, reason domain.AgentRunErrorCode) {
	for _, runID := range runIDs {
		slog.InfoContext(ctx, "已取消客服会话 Agent 运行",
			"agent_run_id", runID,
			"service_session_id", session.ID,
			"conversation_id", session.ConversationID,
			"reason", reason,
		)
	}
}

//go:build server

// Package servicehandoff 把服务周期从 AI 员工交给人工：转人工事件与对客话术、转人工去向解析，失去接待资格的负责人所负责周期退回队列，以及退回后的承接分配与对客通知任务。
package servicehandoff

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"uuid"

	"github.com/runforyou-ai/luway/internal/actions/agentmessage"
	deliveryaction "github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	"github.com/runforyou-ai/luway/internal/actions/chatstate"
	"github.com/runforyou-ai/luway/internal/actions/customernotify"
	"github.com/runforyou-ai/luway/internal/actions/serviceassignment"
	"github.com/runforyou-ai/luway/internal/actions/servicecategory"
	"github.com/runforyou-ai/luway/internal/actions/serviceroute"
	"github.com/runforyou-ai/luway/internal/actions/servicestate"
	"github.com/runforyou-ai/luway/internal/actions/servicesummary"
	"github.com/runforyou-ai/luway/internal/common/logscope"
	"github.com/runforyou-ai/luway/internal/domain"
	serverstorage "github.com/runforyou-ai/luway/internal/storage/server"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/runforyou-ai/support/str"
	"github.com/uptrace/bun"
)

// handoffReasonTextMaxRunes 限制写入转人工事件的原因说明长度。
const handoffReasonTextMaxRunes = 500

// Handoff 描述一次把服务周期从 AI 员工交给人工的事实：调用方锁定的会话、服务周期、服务来源与外发目标；Channel 只在渠道来源取值。
type Handoff struct {
	Conversation    *servermodels.Conversation
	Session         *servermodels.ServiceSession
	Source          domain.ServiceSource
	DeliveryRoute   deliveryaction.Route
	Channel         *servermodels.Channel
	AgentIdentityID string
	Queue           serviceroute.RouteSnapshot // 转人工进入的团队或公共队列。
	Member          *serviceassignment.Member  // 队列中自动分配的承接成员，为空表示留在队列等待领取。
	NoticeKey       string                     // 对客通知的幂等键。
	EventKey        string                     // 转人工系统事件的幂等键。
	Reason          domain.AgentHandoffReason
	ReasonText      string
	Category        *servermodels.ServiceCategory // AI 选择的咨询分类，为空表示未选择或系统转交。
	AgentRunID      *string
}

// Apply 在调用方持有会话锁的事务中写入转人工事件，渠道来源追加按承接结果生成的对客通知，其他来源追加发起人可见的服务进度，按去向更新负责人与团队；返回的对客通知或服务进度已推进会话版本并通知全部受众。
func Apply(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, emailSender customernotify.Sender, handoff Handoff) (*servermodels.Message, error) {
	session := handoff.Session
	participantID, err := agentmessage.EnsureCustomerParticipant(ctx, db, session.WorkspaceID, session.ConversationID, handoff.AgentIdentityID)
	if err != nil {
		return nil, err
	}
	var agentName string
	if err := db.NewSelect().Model((*servermodels.WorkspaceIdentity)(nil)).Column("display_name").
		Where("oi.workspace_id = ? AND oi.id = ?", session.WorkspaceID, handoff.AgentIdentityID).
		Scan(ctx, &agentName); err != nil {
		return nil, fmt.Errorf("load handoff agent name: %w", err)
	}
	// 自动分配到成员时，事件去向与负责人都取实际承接成员，所属队列保持解析结果。
	target := handoff.Queue.Target()
	var assigneeID, assigneeName *string
	if handoff.Member != nil {
		target = domain.ServiceSessionTarget{Kind: domain.ServiceSessionTargetMember, IdentityID: &handoff.Member.IdentityID, DisplayName: &handoff.Member.DisplayName}
		assigneeID, assigneeName = &handoff.Member.IdentityID, &handoff.Member.DisplayName
	}
	// 写入时刻取持有会话锁之后的数据库时刻。
	now, err := serverstorage.ClockNow(ctx, db)
	if err != nil {
		return nil, err
	}
	var categoryID, categoryName *string
	if handoff.Category != nil {
		categoryID, categoryName = &handoff.Category.ID, &handoff.Category.Name
	}
	// 原因说明截断到固定长度，只进入成员可见的系统事件。
	payload, err := json.Marshal(domain.ServiceSessionHandedOffEvent{
		ServiceSessionID: session.ID, FromIdentityID: handoff.AgentIdentityID, FromDisplayName: agentName,
		Target: target, Reason: handoff.Reason, ReasonText: str.Substr(strings.TrimSpace(handoff.ReasonText), 0, handoffReasonTextMaxRunes), CategoryName: categoryName, AgentRunID: handoff.AgentRunID,
	})
	if err != nil {
		return nil, fmt.Errorf("encode service session handoff event: %w", err)
	}
	eventType := string(domain.ConversationSystemEventServiceSessionHandedOff)
	// 系统事件先于对客通知写入，会话最后消息保持为对客文本；首次写入事件时准备交接摘要。
	event, inserted, err := agentmessage.Append(ctx, db, enqueuer, handoff.Conversation, &servermodels.Message{
		ID: uuid.NewV7().String(), WorkspaceID: session.WorkspaceID, ConversationID: session.ConversationID,
		ServiceSessionID: &session.ID, Type: string(domain.MessageTypeSystem), Visibility: string(domain.MessageVisibilityInternal),
		SystemEventType: &eventType, SystemEventPayload: payload, IdempotencyKey: &handoff.EventKey,
	})
	if err != nil {
		return nil, fmt.Errorf("append service session handoff event: %w", err)
	}
	if inserted {
		if err := servicesummary.MarkHandedOff(ctx, db, enqueuer, session, event.ID); err != nil {
			return nil, err
		}
	}
	// 对客通知不结束客户等待，交接后保留通知前的等待起点。
	awaitingReplySince := session.AwaitingReplySince
	var message *servermodels.Message
	if handoff.Channel != nil {
		notice, err := customerHandoffNotice(ctx, db, emailSender, handoff.Channel, session.ConversationID, assigneeName)
		if err != nil {
			return nil, err
		}
		message, err = agentmessage.AppendCustomer(ctx, db, enqueuer, handoff.Conversation, session, handoff.DeliveryRoute, &servermodels.Message{
			ID: uuid.NewV7().String(), WorkspaceID: session.WorkspaceID, ConversationID: session.ConversationID,
			ServiceSessionID: &session.ID, SenderParticipantID: &participantID,
			Type: string(domain.MessageTypeText), Body: notice, IdempotencyKey: &handoff.NoticeKey,
		})
		if err != nil {
			return nil, fmt.Errorf("append customer handoff notice: %w", err)
		}
	} else {
		// 发起人先看到已转交的队列，自动分配到成员时再看到该成员处理中。
		if message, err = chatstate.AppendRequesterStatus(ctx, db, enqueuer, handoff.Conversation, session, handoff.Source, domain.ServiceRequestStatusHandedOff, new(handoff.Queue.Target()), nil); err != nil {
			return nil, err
		}
		if handoff.Member != nil {
			if message, err = chatstate.AppendRequesterStatus(ctx, db, enqueuer, handoff.Conversation, session, handoff.Source, domain.ServiceRequestStatusProcessing, &target, nil); err != nil {
				return nil, err
			}
		}
	}
	// 周期进入转人工队列，客户在等待时保留等待起点，否则以交接时间为起点；AI 选择了咨询分类时记到周期上；自动分配到成员时由该成员负责。
	transition := servicestate.Begin(session).Handoff(handoff.Queue.TeamID, awaitingReplySince, categoryID, now)
	if assigneeID != nil {
		transition.Assign(*assigneeID, now)
	}
	if err := transition.Save(ctx, db, enqueuer); err != nil {
		return nil, err
	}
	if handoff.Member != nil {
		if err := serviceassignment.MarkAssigned(ctx, db, enqueuer, session, handoff.Member); err != nil {
			return nil, err
		}
	}
	slog.InfoContext(logscope.WithWorkspace(ctx, session.WorkspaceID), "客户会话已由 AI 员工转交人工",
		"conversation_id", session.ConversationID,
		"service_session_id", session.ID, "agent_identity_id", handoff.AgentIdentityID,
		"target_kind", target.Kind, "reason", handoff.Reason, "category_id", categoryID)
	return message, nil
}

// Route 是进入会话锁之前解析出的转人工去向；Channel 只在渠道来源取值。
type Route struct {
	Channel  *servermodels.Channel
	Queue    serviceroute.RouteSnapshot
	Member   *serviceassignment.Member     // 队列中挑选并锁定的可分配成员。
	Category *servermodels.ServiceCategory // 按编号复核仍未归档的咨询分类。
}

// ResolveRoute 在进入会话锁之前解析转人工去向，并锁定队列中挑选的可分配成员；categoryID 为空表示未选择分类。
func ResolveRoute(ctx context.Context, db bun.IDB, workspaceID, conversationID, serviceSessionID, agentIdentityID, categoryID string) (Route, error) {
	resolved, err := resolveAgentHandoffQueue(ctx, db, workspaceID, conversationID, agentIdentityID, categoryID)
	if err != nil {
		return resolved, err
	}
	resolved.Member, err = serviceassignment.LockQueueMember(ctx, db, workspaceID, serviceSessionID, resolved.Queue.TeamID, "")
	return resolved, err
}

// resolveAgentHandoffQueue 按 AI 员工交出周期的去向规则解析队列，团队取 FOR KEY SHARE：依次取咨询分类团队、入口失败团队（渠道来源取渠道失败团队，其他来源取 AI 员工的转人工团队）与公共队列；categoryID 为空表示未选择分类。
func resolveAgentHandoffQueue(ctx context.Context, db bun.IDB, workspaceID, conversationID, agentIdentityID, categoryID string) (Route, error) {
	resolved := Route{}
	service, err := chatstate.LoadServiceConversation(ctx, db, workspaceID, conversationID)
	if err != nil {
		return resolved, err
	}
	var fallbackTeamID *string
	if domain.ServiceSource(service.Source) == domain.ServiceSourceChannel {
		if resolved.Channel, err = serviceroute.LoadConversationChannel(ctx, db, workspaceID, conversationID); err != nil {
			return resolved, err
		}
		fallbackTeamID = serviceroute.ChannelHandoffTeamID(resolved.Channel)
	} else if err := db.NewSelect().Model((*servermodels.Agent)(nil)).Column("handoff_team_id").
		Where("workspace_id = ? AND identity_id = ?", workspaceID, agentIdentityID).
		Scan(ctx, &fallbackTeamID); err != nil {
		return resolved, fmt.Errorf("load agent handoff team: %w", err)
	}
	// 分类在模型选择后被归档时按未选择分类处理。
	var categoryTeamID *string
	if categoryID != "" {
		if resolved.Category, err = servicecategory.FindActive(ctx, db, workspaceID, categoryID); err != nil {
			return resolved, err
		}
		if resolved.Category != nil {
			categoryTeamID = resolved.Category.TeamID
		}
	}
	resolved.Queue, err = serviceroute.ResolveHandoffQueue(ctx, db, workspaceID, categoryTeamID, fallbackTeamID, true)
	return resolved, err
}

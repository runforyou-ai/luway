//go:build server

// Package notificationtask 登记用户通知任务：新消息在已读确认窗口后生成通知，客服处理周期提醒立即生成通知，生成的通知按接收账号登记离线推送。
package notificationtask

import (
	"context"
	"fmt"
	"time"
	"uuid"

	"github.com/runforyou-ai/luway/internal/domain"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

const (
	// MessageActionName 为一条新消息生成用户通知。
	MessageActionName = "notification.message"
	// ServiceAttentionActionName 为一次客服处理周期提醒生成用户通知。
	ServiceAttentionActionName = "notification.service_attention"
	// ToolDecisionActionName 为一次等待确认或审批的工具调用生成用户通知。
	ToolDecisionActionName = "notification.tool_decision"
	// PushActionName 把一条用户通知推送到接收账号在同一应用与平台上已登录的一组移动设备。
	PushActionName = "notification.push"
	// MessageSettleDelay 是新消息的已读确认窗口：窗口内在任一设备读到该消息的成员不再收到通知。
	MessageSettleDelay = 2 * time.Second
	// taskMaxAttempts 是通知任务的最大尝试次数。
	taskMaxAttempts = 3
)

// MessageInput 定义新消息通知任务的输入。
type MessageInput struct {
	WorkspaceID    string `json:"workspaceId"`
	ConversationID string `json:"conversationId"`
	MessageID      string `json:"messageId"`
}

// ServiceAttentionInput 定义客服处理周期提醒通知任务的输入；NotificationID 在登记时生成，重试时保持不变。
type ServiceAttentionInput struct {
	WorkspaceID      string                        `json:"workspaceId"`
	UserID           string                        `json:"userId"`
	ConversationID   string                        `json:"conversationId"`
	ServiceSessionID string                        `json:"serviceSessionId"`
	Reason           domain.ServiceAttentionReason `json:"reason"`
	NotificationID   string                        `json:"notificationId"`
}

// ToolDecisionInput 定义工具调用确认或审批通知任务的输入。
type ToolDecisionInput struct {
	WorkspaceID string `json:"workspaceId"`
	UserID      string `json:"userId"`
	ToolCallID  string `json:"toolCallId"`
}

// PushInput 定义离线推送任务的输入：接收成员与账号、目标应用与平台上的设备，以及已按接收人语言生成的通知。
type PushInput struct {
	AccountID      string                  `json:"accountId"`
	UserID         string                  `json:"userId"`
	App            string                  `json:"app"`
	Platform       domain.PushPlatform     `json:"platform"`
	DeviceIDs      []string                `json:"deviceIds"`
	WorkspaceID    string                  `json:"workspaceId"`
	NotificationID string                  `json:"notificationId"`
	ConversationID string                  `json:"conversationId"`
	View           domain.NotificationView `json:"view"`
	Title          string                  `json:"title"`
	Body           string                  `json:"body"`
}

// PushRequest 返回一条用户通知的离线推送任务投递请求，任务按通知、接收账号、应用与平台幂等。
func PushRequest(input PushInput) servertask.EnqueueRequest {
	return servertask.EnqueueRequest{ActionName: PushActionName, Payload: input, Options: servertask.EnqueueOptions{
		WorkspaceID: input.WorkspaceID, MaxAttempts: taskMaxAttempts,
		IdempotencyKey: "notification-push:" + input.NotificationID + ":" + input.AccountID + ":" + input.App + ":" + string(input.Platform),
	}}
}

// EnqueueMessage 在消息写入事务内登记该消息的通知任务，不计入未读与提醒的消息不登记；任务按消息编号幂等，在已读确认窗口后执行。
func EnqueueMessage(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, message *servermodels.Message) error {
	if !domain.CountsTowardUnread(domain.MessageType(message.Type), domain.MessageVisibility(message.Visibility)) {
		return nil
	}
	input := MessageInput{WorkspaceID: message.WorkspaceID, ConversationID: message.ConversationID, MessageID: message.ID}
	if _, err := enqueuer.EnqueueIn(ctx, MessageActionName, input, servertask.EnqueueOptions{
		WorkspaceID: message.WorkspaceID, MaxAttempts: taskMaxAttempts, IdempotencyKey: "notification-message:" + message.ID,
		Delay: MessageSettleDelay,
	}); err != nil {
		return fmt.Errorf("enqueue message notification: %w", err)
	}
	return nil
}

// EnqueueServiceAttention 在提醒所在事务内登记一次客服处理周期提醒的通知任务。
func EnqueueServiceAttention(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, session *servermodels.ServiceSession, userID string, reason domain.ServiceAttentionReason) error {
	input := ServiceAttentionInput{
		WorkspaceID: session.WorkspaceID, UserID: userID, ConversationID: session.ConversationID, ServiceSessionID: session.ID,
		Reason: reason, NotificationID: "service_attention:" + uuid.NewV7().String(),
	}
	if _, err := enqueuer.EnqueueIn(ctx, ServiceAttentionActionName, input, servertask.EnqueueOptions{
		WorkspaceID: session.WorkspaceID, MaxAttempts: taskMaxAttempts,
	}); err != nil {
		return fmt.Errorf("enqueue service attention notification: %w", err)
	}
	return nil
}

// EnqueueToolDecision 在提交工具调用的事务内登记通知处理成员确认或审批的任务，任务按工具调用编号幂等。
func EnqueueToolDecision(ctx context.Context, db bun.IDB, enqueuer servertask.TxEnqueuer, workspaceID, userID, toolCallID string) error {
	input := ToolDecisionInput{WorkspaceID: workspaceID, UserID: userID, ToolCallID: toolCallID}
	if _, err := enqueuer.EnqueueIn(ctx, ToolDecisionActionName, input, servertask.EnqueueOptions{
		WorkspaceID: workspaceID, MaxAttempts: taskMaxAttempts, IdempotencyKey: "notification-tool-decision:" + toolCallID,
	}); err != nil {
		return fmt.Errorf("enqueue tool decision notification: %w", err)
	}
	return nil
}

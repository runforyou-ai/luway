//go:build server

// 消息读取、游标解析与消息响应转换。
package direct

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"

	conversationaction "github.com/runforyou-ai/cervi/internal/actions/conversation"
	"github.com/runforyou-ai/cervi/internal/appservice"
	"github.com/runforyou-ai/cervi/internal/common"
	"github.com/runforyou-ai/cervi/internal/domain"
	"github.com/runforyou-ai/cervi/internal/i18n"
	servermodels "github.com/runforyou-ai/cervi/internal/storage/server/models"
)

// ListConversationMessages 返回成员可见的会话消息。
func (o *directOperations) ListConversationMessages(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ConversationMessageListInput) (appservice.ConversationMessageList, error) {
	before, after, err := decodeMessageCursors(conversationID, input.Before, input.After)
	if err != nil {
		return appservice.ConversationMessageList{}, conversationMessageError(ctx, meta, err, identity.Organization.ID, conversationID)
	}
	history, err := o.listConversationMessages.Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID, Before: before, After: after})
	if err != nil {
		return appservice.ConversationMessageList{}, conversationMessageError(ctx, meta, err, identity.Organization.ID, conversationID)
	}
	return o.conversationMessageListFromAction(ctx, meta, identity, conversationID, history)
}

// ReadConversationMessageWindow 重读已加载首尾游标之间的完整消息范围。
func (o *directOperations) ReadConversationMessageWindow(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ConversationMessageWindowInput) (appservice.ConversationMessageList, error) {
	start, validStart := decodeConversationMessageCursor(input.Start, conversationID)
	end, validEnd := decodeConversationMessageCursor(input.End, conversationID)
	if !validStart || !validEnd || start.MessageSeq > end.MessageSeq {
		return appservice.ConversationMessageList{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, map[string]i18n.Key{"cursor": i18n.FieldMessageCursorInvalid})
	}
	history, err := o.listConversationMessages.Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID, Start: &start, End: &end})
	if err != nil {
		return appservice.ConversationMessageList{}, conversationMessageError(ctx, meta, err, identity.Organization.ID, conversationID)
	}
	return o.conversationMessageListFromAction(ctx, meta, identity, conversationID, history)
}

// conversationMessageFromAction 转换成员会话消息契约。
func conversationMessageFromAction(message conversationaction.ConversationMessage, avatarURLs map[string]string) appservice.ConversationMessage {
	sender := conversationMessageSenderFromAction(message.Sender, avatarURLs)
	var sessionStart *appservice.ConversationMessageSessionStart
	if message.SessionStart != nil {
		sessionStart = &appservice.ConversationMessageSessionStart{
			Sequence:  message.SessionStart.Sequence,
			StartedAt: message.SessionStart.StartedAt,
			Status:    appservice.ServiceSessionStatus(message.SessionStart.Status),
		}
	}
	var systemEvent *appservice.ConversationSystemEvent
	if message.SystemEvent != nil {
		targets := make([]appservice.ConversationSystemEventParticipant, 0, len(message.SystemEvent.Targets))
		for _, target := range message.SystemEvent.Targets {
			targets = append(targets, appservice.ConversationSystemEventParticipant{IdentityID: target.IdentityID, DisplayName: target.DisplayName, AssistantOwnerName: target.AssistantOwnerName})
		}
		systemEvent = &appservice.ConversationSystemEvent{
			Type: appservice.ConversationSystemEventType(message.SystemEvent.Type),
			Actor: appservice.ConversationSystemEventParticipant{
				IdentityID: message.SystemEvent.Actor.IdentityID, DisplayName: message.SystemEvent.Actor.DisplayName,
				AssistantOwnerName: message.SystemEvent.Actor.AssistantOwnerName,
			},
			Targets: targets, PreviousTitle: message.SystemEvent.PreviousTitle, Title: message.SystemEvent.Title,
			ServiceSessionID: message.SystemEvent.ServiceSessionID, FromIdentityID: message.SystemEvent.FromIdentityID,
			FromDisplayName: message.SystemEvent.FromDisplayName, HandoffReason: (*appservice.AgentHandoffReason)(message.SystemEvent.Reason),
			ReturnReason: (*appservice.ServiceSessionReturnReason)(message.SystemEvent.ReturnReason),
			CloseReason:  (*appservice.ServiceSessionCloseReason)(message.SystemEvent.CloseReason),
			ReasonText:   message.SystemEvent.ReasonText, CategoryName: message.SystemEvent.CategoryName, AgentRunID: message.SystemEvent.AgentRunID,
			RatingResolved: message.SystemEvent.Resolved, RatingComment: message.SystemEvent.Comment, Email: message.SystemEvent.Email,
			ServiceStatus: (*appservice.ServiceRequestStatus)(message.SystemEvent.Status),
		}
		// 客服处理周期事件的操作人按群聊事件的 actor 结构返回。
		if message.SystemEvent.ActorIdentityID != nil && message.SystemEvent.ActorDisplayName != nil {
			systemEvent.Actor = appservice.ConversationSystemEventParticipant{IdentityID: *message.SystemEvent.ActorIdentityID, DisplayName: *message.SystemEvent.ActorDisplayName}
		}
		if target := message.SystemEvent.Target; target != nil {
			systemEvent.SessionTarget = &appservice.ServiceSessionTarget{Kind: appservice.ServiceSessionTargetKind(target.Kind),
				TeamID: target.TeamID, TeamName: target.TeamName, IdentityID: target.IdentityID, DisplayName: target.DisplayName}
		}
	}
	var replyTo *appservice.ConversationMessageReference
	if message.ReplyTo != nil {
		replyTo = &appservice.ConversationMessageReference{
			ExternalSenderName: message.ReplyTo.ExternalSenderName, ID: message.ReplyTo.ID, Type: appservice.MessageType(message.ReplyTo.Type), Visibility: appservice.MessageVisibility(message.ReplyTo.Visibility), Body: message.ReplyTo.Body, Deleted: message.ReplyTo.Deleted,
			Sender: conversationMessageSenderFromAction(message.ReplyTo.Sender, avatarURLs),
		}
	}
	mentions := make([]appservice.ConversationMessageMention, 0, len(message.Mentions))
	for _, mention := range message.Mentions {
		mentions = append(mentions, appservice.ConversationMessageMention{
			ChatSubjectID: mention.ChatSubjectID, Kind: appservice.ChatSubjectKind(mention.Kind),
			SourceID: mention.SourceID, DisplayName: mention.DisplayName,
		})
	}
	var attachment *appservice.MessageAttachment
	if message.Attachment != nil {
		attachment = &appservice.MessageAttachment{
			File:           appservice.File{ID: message.Attachment.ID, Name: message.Attachment.Name, ContentType: message.Attachment.ContentType, ByteSize: message.Attachment.ByteSize},
			TransferStatus: appservice.MessageAttachmentTransferStatus(message.Attachment.TransferStatus),
			ImageWidth:     message.Attachment.ImageWidth, ImageHeight: message.Attachment.ImageHeight,
		}
	}
	var delivery *appservice.CustomerMessageDelivery
	if message.Delivery != nil {
		delivery = &appservice.CustomerMessageDelivery{
			CanRetry: message.Delivery.CanRetry, Paused: message.Delivery.Paused, ID: message.Delivery.ID,
			Status: appservice.CustomerDeliveryStatus(message.Delivery.Status), LastError: message.Delivery.LastError,
		}
	}
	var translation *appservice.ConversationMessageTranslation
	if message.Translation != nil {
		translation = &appservice.ConversationMessageTranslation{Language: message.Translation.Language, Body: message.Translation.Body}
	}
	// 文本和附件消息可以被引用；对客回复只能引用对客可见且渠道能够投递该引用的消息。
	quotable := message.Type == domain.MessageTypeText || message.Type == domain.MessageTypeAttachment
	return appservice.ConversationMessage{
		CanReply:        quotable && !message.ReplyUnavailable && message.Visibility == domain.MessageVisibilityShared,
		CanNoteReply:    quotable,
		ClientMessageID: message.ClientMessageID,
		Attachment:      attachment,
		AgentProcess:    conversationAgentProcessFromAction(message.AgentProcess),
		AgentErrorCode:  (*string)(message.AgentErrorCode),
		ID:              message.ID, Type: appservice.MessageType(message.Type), Visibility: appservice.MessageVisibility(message.Visibility), Body: message.Body,
		Language: common.StringValue(message.Language), Translation: translation,
		OriginatedAt: message.OriginatedAt, SourceOrder: message.SourceOrder, CreatedAt: message.CreatedAt, MessageSeq: strconv.FormatInt(message.MessageSeq, 10),
		Sender: sender, SessionStart: sessionStart, SystemEvent: systemEvent,
		ReplyTo: replyTo, Mentions: mentions, MentionAll: message.MentionAll, Delivery: delivery,
	}
}

// conversationMessageSenderFromAction 转换消息发送主体。
func conversationMessageSenderFromAction(sender *conversationaction.ConversationMessageSender, avatarURLs map[string]string) *appservice.ConversationMessageSender {
	if sender == nil {
		return nil
	}
	return &appservice.ConversationMessageSender{
		ChatSubjectID: sender.ChatSubjectID, Kind: appservice.ChatSubjectKind(sender.Kind),
		SourceID: sender.SourceID, DisplayName: sender.DisplayName, ContactNumber: sender.ContactNumber,
		AvatarURL: optionalFileURL(avatarURLs, sender.AvatarFileID), IdentityType: (*appservice.OrganizationIdentityType)(sender.IdentityType),
		AssistantOwnerName: sender.AssistantOwnerName,
	}
}

// encodeConversationMessageCursor 编码绑定会话的消息序号与定位编号。
func encodeConversationMessageCursor(conversationID string, point conversationaction.MessageCursorPoint) string {
	return conversationID + "." + strconv.FormatInt(point.MessageSeq, 10) + "." + point.ID
}

// decodeMessageCursors 解码非空的向前与向后消息游标：两者同时给出时返回 cursor 字段错误，游标不合法时返回以字段名标记的消息校验错误。
func decodeMessageCursors(conversationID, before, after string) (*conversationaction.MessageCursorPoint, *conversationaction.MessageCursorPoint, error) {
	if before != "" && after != "" {
		return nil, nil, &conversationaction.ValidationError{Fields: map[string]conversationaction.ValidationCode{"cursor": conversationaction.ValidationCursorInvalid}}
	}
	points := [2]*conversationaction.MessageCursorPoint{}
	for index, cursor := range [2]struct{ field, value string }{{"before", before}, {"after", after}} {
		if cursor.value == "" {
			continue
		}
		point, valid := decodeConversationMessageCursor(cursor.value, conversationID)
		if !valid {
			return nil, nil, &conversationaction.ValidationError{Fields: map[string]conversationaction.ValidationCode{cursor.field: conversationaction.ValidationCursorInvalid}}
		}
		points[index] = &point
	}
	return points[0], points[1], nil
}

// decodeConversationMessageCursor 校验消息游标的会话、序号和定位编号。
func decodeConversationMessageCursor(value, conversationID string) (conversationaction.MessageCursorPoint, bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != conversationID || !common.ValidUUID(parts[2]) {
		return conversationaction.MessageCursorPoint{}, false
	}
	sequence, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || sequence <= 0 {
		return conversationaction.MessageCursorPoint{}, false
	}
	return conversationaction.MessageCursorPoint{ID: parts[2], MessageSeq: sequence}, true
}

// conversationMessageError 转换成员消息读取错误。
func conversationMessageError(ctx context.Context, meta appservice.RequestMeta, err error, organizationID, conversationID string) error {
	if mapped := commonActionError(ctx, meta, err); mapped != nil {
		return mapped
	}
	if errors.Is(err, conversationaction.ErrConversationNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorConversationNotFound).WithReason("conversation_unavailable")
	}
	if errors.Is(err, conversationaction.ErrMessageUnavailable) {
		return appservice.NotFoundError(meta, i18n.ErrorConversationMessageUnavailable).WithReason("message_unavailable")
	}
	if errors.Is(err, conversationaction.ErrMentionTargetInvalid) {
		return appservice.InvalidError(meta, i18n.ErrorConversationMentionTargetInvalid, nil)
	}
	if validationError, ok := errors.AsType[*conversationaction.ValidationError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, conversationMessageValidationKeys))
	}
	slog.Warn("读取会话消息失败", "organization_id", organizationID, "conversation_id", conversationID, "error", err)
	return appservice.FailedError(meta, i18n.ErrorConversationMessageListFailed)
}

// conversationMessageListFromAction 共用成员消息窗口及游标转换。
func (o *directOperations) conversationMessageListFromAction(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, history conversationaction.ConversationMessageHistory) (appservice.ConversationMessageList, error) {
	agentAvatarFileIDs := make([]*string, 0, len(history.PendingAgents)+len(history.AgentRuns))
	for _, run := range history.AgentRuns {
		agentAvatarFileIDs = append(agentAvatarFileIDs, run.AgentAvatarFileID)
	}
	for _, agent := range history.PendingAgents {
		agentAvatarFileIDs = append(agentAvatarFileIDs, agent.AvatarFileID)
	}
	avatarURLs, err := o.conversationAvatarURLs(ctx, identity, history.Messages, agentAvatarFileIDs...)
	if err != nil {
		return appservice.ConversationMessageList{}, conversationMessageError(ctx, meta, err, identity.Organization.ID, conversationID)
	}
	result := appservice.ConversationMessageList{HasEarlier: history.HasEarlier, HasLater: history.HasLater, Messages: make([]appservice.ConversationMessage, 0, len(history.Messages))}
	result.AgentRuns = make([]appservice.ConversationAgentRun, 0, len(history.AgentRuns))
	for _, run := range history.AgentRuns {
		result.AgentRuns = append(result.AgentRuns, appservice.ConversationAgentRun{ID: run.ID, AgentIdentityID: run.AgentIdentityID,
			AgentName: run.AgentName, AgentAssistantOwnerName: run.AgentAssistantOwnerName, AgentAvatarURL: optionalFileURL(avatarURLs, run.AgentAvatarFileID),
			Status: appservice.AgentRunStatus(run.Status), ErrorCode: run.ErrorCode, LastError: run.LastError,
			Process: conversationAgentProcessFromAction(run.Process), ExecutionDeviceID: run.ExecutionDeviceID, ExecutionDeviceName: run.ExecutionDeviceName})
	}
	result.PendingAgents = make([]appservice.ConversationPendingAgent, 0, len(history.PendingAgents))
	for _, agent := range history.PendingAgents {
		result.PendingAgents = append(result.PendingAgents, appservice.ConversationPendingAgent{
			IdentityID: agent.IdentityID, DisplayName: agent.DisplayName, AvatarURL: optionalFileURL(avatarURLs, agent.AvatarFileID),
			AssistantOwnerName: agent.AssistantOwnerName,
		})
	}
	for _, message := range history.Messages {
		result.Messages = append(result.Messages, conversationMessageFromAction(message, avatarURLs))
	}
	if history.Before != nil {
		value := encodeConversationMessageCursor(conversationID, *history.Before)
		result.Before = &value
	}
	if history.After != nil {
		value := encodeConversationMessageCursor(conversationID, *history.After)
		result.After = &value
	}
	return result, nil
}

// conversationAvatarURLs 批量解析消息发送者、引用发送者、单聊目标和运行中 Agent 的头像。
func (o *directOperations) conversationAvatarURLs(ctx context.Context, identity *servermodels.Identity, messages []conversationaction.ConversationMessage, extraFileIDs ...*string) (map[string]string, error) {
	fileIDs := make([]string, 0, len(messages)+len(extraFileIDs))
	for _, fileID := range extraFileIDs {
		if fileID != nil {
			fileIDs = append(fileIDs, *fileID)
		}
	}
	for _, message := range messages {
		if message.Sender != nil && message.Sender.AvatarFileID != nil {
			fileIDs = append(fileIDs, *message.Sender.AvatarFileID)
		}
		if message.ReplyTo != nil && message.ReplyTo.Sender != nil && message.ReplyTo.Sender.AvatarFileID != nil {
			fileIDs = append(fileIDs, *message.ReplyTo.Sender.AvatarFileID)
		}
	}
	return o.activeFileURLs(ctx, identity, fileIDs)
}

// conversationMessageWithAvatar 转换发送结果并补充头像地址。
func (o *directOperations) conversationMessageWithAvatar(ctx context.Context, identity *servermodels.Identity, message conversationaction.ConversationMessage) appservice.ConversationMessage {
	urls, err := o.conversationAvatarURLs(ctx, identity, []conversationaction.ConversationMessage{message})
	if err != nil {
		slog.Warn("读取已保存消息头像失败", "organization_id", identity.Organization.ID, "message_id", message.ID, "error", err)
	}
	return conversationMessageFromAction(message, urls)
}

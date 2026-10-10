//go:build server

// 消息读取、游标解析与消息响应转换。

package direct

import (
	"context"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/runforyou-ai/luway/internal/actions/agentprocess/processquery"
	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/str"
)

// ListConversationMessages 返回成员可见的会话消息。
func (o *conversationOps) ListConversationMessages(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ConversationMessageListInput) (appservice.ConversationMessageList, error) {
	before, after, err := decodeMessageCursors(conversationID, input.Before, input.After)
	if err != nil {
		return appservice.ConversationMessageList{}, conversationMessageError(meta, err)
	}
	history, err := o.listConversationMessages.Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID, Before: before, After: after})
	if err != nil {
		return appservice.ConversationMessageList{}, conversationMessageError(meta, err)
	}
	return o.conversationMessageListFromAction(ctx, meta, identity, conversationID, history)
}

// ReadConversationMessageWindow 重读已加载首尾游标之间的完整消息范围。
func (o *conversationOps) ReadConversationMessageWindow(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ConversationMessageWindowInput) (appservice.ConversationMessageList, error) {
	start, validStart := decodeConversationMessageCursor(input.Start, conversationID)
	end, validEnd := decodeConversationMessageCursor(input.End, conversationID)
	if !validStart || !validEnd || start.MessageSeq > end.MessageSeq {
		return appservice.ConversationMessageList{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed, map[string]i18n.Key{"cursor": i18n.FieldMessageCursorInvalid})
	}
	history, err := o.listConversationMessages.Execute(ctx, identity, conversationaction.ConversationMessageHistoryInput{ConversationID: conversationID, Start: &start, End: &end})
	if err != nil {
		return appservice.ConversationMessageList{}, conversationMessageError(meta, err)
	}
	return o.conversationMessageListFromAction(ctx, meta, identity, conversationID, history)
}

// conversationMessageFromAction 转换成员会话消息契约。
func conversationMessageFromAction(ctx context.Context, message conversationaction.ConversationMessage, avatarURLs map[string]string) appservice.ConversationMessage {
	sender := conversationMessageSenderFromAction(message.Sender, avatarURLs)
	var sessionStart *appservice.ConversationMessageSessionStart
	if message.SessionStart != nil {
		sessionStart = &appservice.ConversationMessageSessionStart{
			Sequence:  message.SessionStart.Sequence,
			StartedAt: message.SessionStart.StartedAt,
			Status:    message.SessionStart.Status,
		}
	}
	var systemEvent *appservice.ConversationSystemEvent
	if message.SystemEvent != nil {
		targets := arr.Map(message.SystemEvent.Targets, func(target conversationaction.ConversationSystemEventParticipant) appservice.ConversationSystemEventParticipant {
			return appservice.ConversationSystemEventParticipant{IdentityID: target.IdentityID, DisplayName: target.DisplayName, PersonalResponsibleName: target.PersonalResponsibleName}
		})
		systemEvent = &appservice.ConversationSystemEvent{
			Type: message.SystemEvent.Type,
			Actor: appservice.ConversationSystemEventParticipant{
				IdentityID: message.SystemEvent.Actor.IdentityID, DisplayName: message.SystemEvent.Actor.DisplayName,
				PersonalResponsibleName: message.SystemEvent.Actor.PersonalResponsibleName,
			},
			Targets: targets, PreviousTitle: message.SystemEvent.PreviousTitle, Title: message.SystemEvent.Title,
			ServiceSessionID: message.SystemEvent.ServiceSessionID, FromIdentityID: message.SystemEvent.FromIdentityID,
			FromDisplayName: message.SystemEvent.FromDisplayName, HandoffReason: message.SystemEvent.Reason,
			ReturnReason: message.SystemEvent.ReturnReason,
			CloseReason:  message.SystemEvent.CloseReason,
			ReasonText:   message.SystemEvent.ReasonText, CategoryName: message.SystemEvent.CategoryName, AgentRunID: message.SystemEvent.AgentRunID,
			RatingResolved: message.SystemEvent.Resolved, RatingComment: message.SystemEvent.Comment, Email: message.SystemEvent.Email,
			ServiceStatus: message.SystemEvent.Status,
		}
		if call := message.SystemEvent.ToolCall; call != nil {
			systemEvent.ToolCall = new(agentToolDecision(ctx, *call))
		}
		// 客服处理周期事件的操作人按群聊事件的 actor 结构返回。
		if message.SystemEvent.ActorIdentityID != nil && message.SystemEvent.ActorDisplayName != nil {
			systemEvent.Actor = appservice.ConversationSystemEventParticipant{IdentityID: *message.SystemEvent.ActorIdentityID, DisplayName: *message.SystemEvent.ActorDisplayName}
		}
		if target := message.SystemEvent.Target; target != nil {
			systemEvent.SessionTarget = &appservice.ServiceSessionTarget{Kind: target.Kind,
				TeamID: target.TeamID, TeamName: target.TeamName, IdentityID: target.IdentityID, DisplayName: target.DisplayName}
		}
	}
	var replyTo *appservice.ConversationMessageReference
	if message.ReplyTo != nil {
		replyTo = &appservice.ConversationMessageReference{
			ExternalSenderName: message.ReplyTo.ExternalSenderName, ID: message.ReplyTo.ID, Type: message.ReplyTo.Type, Visibility: message.ReplyTo.Visibility, Body: message.ReplyTo.Body, Deleted: message.ReplyTo.Deleted,
			Sender: conversationMessageSenderFromAction(message.ReplyTo.Sender, avatarURLs),
		}
	}
	mentions := arr.Map(message.Mentions, func(mention conversationaction.ConversationMessageMention) appservice.ConversationMessageMention {
		return appservice.ConversationMessageMention{
			ChatSubjectID: mention.ChatSubjectID, Kind: mention.Kind,
			SourceID: mention.SourceID, DisplayName: mention.DisplayName,
		}
	})
	var attachment *appservice.MessageAttachment
	if message.Attachment != nil {
		attachment = &appservice.MessageAttachment{
			File:           appservice.File{ID: message.Attachment.ID, Name: message.Attachment.Name, ContentType: message.Attachment.ContentType, ByteSize: message.Attachment.ByteSize},
			TransferStatus: message.Attachment.TransferStatus,
			ImageWidth:     message.Attachment.ImageWidth, ImageHeight: message.Attachment.ImageHeight,
		}
	}
	var delivery *appservice.ChannelMessageDelivery
	if message.Delivery != nil {
		delivery = &appservice.ChannelMessageDelivery{
			CanRetry: message.Delivery.CanRetry, Paused: message.Delivery.Paused, ID: message.Delivery.ID,
			Status: message.Delivery.Status, LastError: message.Delivery.LastError,
			PartiallySent: message.Delivery.PartiallySent,
		}
	}
	translation := support.MapPtr(message.Translation, func(translation conversationaction.MessageTranslation) appservice.ConversationMessageTranslation {
		return appservice.ConversationMessageTranslation{Language: translation.Language, Body: translation.Body}
	})
	// 文本和附件消息可以被引用；对客回复只能引用对客可见且渠道能够投递该引用的消息。
	quotable := message.Type == domain.MessageTypeText || message.Type == domain.MessageTypeAttachment
	return appservice.ConversationMessage{
		CanReply:        quotable && !message.ReplyUnavailable && message.Visibility == domain.MessageVisibilityShared,
		CanNoteReply:    quotable,
		ClientMessageID: message.ClientMessageID,
		Attachment:      attachment,
		AgentProcess:    support.MapPtr(message.AgentProcess, conversationAgentProcessFromAction),
		AgentErrorCode:  (*string)(message.AgentErrorCode),
		LocalAgentReply: support.MapPtr(message.LocalAgentReply, conversationLocalAgentTurn),
		LocalAgentTurns: arr.Map(message.LocalAgentTurns, conversationLocalAgentTurn),
		ID:              message.ID, Type: message.Type, Visibility: message.Visibility, Body: message.Body,
		Language: support.Deref(message.Language), Translation: translation,
		OriginatedAt: message.OriginatedAt, CreatedAt: message.CreatedAt, MessageSeq: strconv.FormatInt(message.MessageSeq, 10),
		Sender: sender, SessionStart: sessionStart, SystemEvent: systemEvent,
		ReplyTo: replyTo, Mentions: mentions, MentionAll: message.MentionAll, Delivery: delivery,
	}
}

// conversationMessageSenderFromAction 转换消息发送主体。
func conversationMessageSenderFromAction(sender *conversationaction.ConversationMessageSender, avatarURLs map[string]string) *appservice.ConversationMessageSender {
	return support.MapPtr(sender, func(sender conversationaction.ConversationMessageSender) appservice.ConversationMessageSender {
		return appservice.ConversationMessageSender{
			ChatSubjectID: sender.ChatSubjectID, Kind: sender.Kind,
			SourceID: sender.SourceID, DisplayName: sender.DisplayName, ContactNumber: sender.ContactNumber,
			AvatarURL: optionalFileURL(avatarURLs, sender.AvatarFileID), IdentityType: sender.IdentityType,
			PersonalResponsibleName: sender.PersonalResponsibleName,
		}
	})
}

// encodeConversationMessageCursor 编码绑定会话的消息序号与定位编号，没有位置时返回空。
func encodeConversationMessageCursor(conversationID string, point *conversationaction.MessageCursorPoint) *string {
	return support.MapPtr(point, func(point conversationaction.MessageCursorPoint) string {
		return conversationID + "." + strconv.FormatInt(point.MessageSeq, 10) + "." + point.ID
	})
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
	if len(parts) != 3 || parts[0] != conversationID || !str.IsUUID(parts[2]) {
		return conversationaction.MessageCursorPoint{}, false
	}
	sequence, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || sequence <= 0 {
		return conversationaction.MessageCursorPoint{}, false
	}
	return conversationaction.MessageCursorPoint{ID: parts[2], MessageSeq: sequence}, true
}

// conversationMessageErrors 是成员消息读取的错误转换规则。
var conversationMessageErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.Is(conversationaction.ErrConversationNotFound, dispatch.WithReason(dispatch.NotFound(i18n.ErrorConversationNotFound), "conversation_unavailable")),
	dispatch.Is(conversationaction.ErrMessageUnavailable, dispatch.WithReason(dispatch.NotFound(i18n.ErrorConversationMessageUnavailable), "message_unavailable")),
	dispatch.Is(conversationaction.ErrMentionTargetInvalid, dispatch.Invalid(i18n.ErrorConversationMentionTargetInvalid)),
	dispatch.FieldRule(conversationMessageValidationKeys),
})

// conversationMessageError 转换成员消息读取错误。
func conversationMessageError(meta appservice.RequestMeta, err error) error {
	return conversationMessageErrors.Translate(meta, err, i18n.ErrorConversationMessageListFailed)
}

// conversationMessageListFromAction 共用成员消息窗口及游标转换。
func (o *conversationOps) conversationMessageListFromAction(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, history conversationaction.ConversationMessageHistory) (appservice.ConversationMessageList, error) {
	agentAvatarFileIDs := slices.Concat(
		arr.Map(history.AgentRuns, func(run processquery.Run) *string { return run.AgentAvatarFileID }),
		arr.Map(history.PendingAgents, func(agent processquery.PendingAgent) *string { return agent.AvatarFileID }),
	)
	avatarURLs, err := o.conversationAvatarURLs(ctx, identity, history.Messages, agentAvatarFileIDs...)
	if err != nil {
		return appservice.ConversationMessageList{}, conversationMessageError(meta, err)
	}
	result := appservice.ConversationMessageList{
		HasEarlier: history.HasEarlier, HasLater: history.HasLater,
		Messages: arr.Map(history.Messages, func(message conversationaction.ConversationMessage) appservice.ConversationMessage {
			return conversationMessageFromAction(ctx, message, avatarURLs)
		}),
		AgentRuns: arr.Map(history.AgentRuns, func(run processquery.Run) appservice.ConversationAgentRun {
			return appservice.ConversationAgentRun{ID: run.ID, AgentIdentityID: run.AgentIdentityID,
				AgentName: run.AgentName, AgentPersonalResponsibleName: run.AgentPersonalResponsibleName, AgentAvatarURL: optionalFileURL(avatarURLs, run.AgentAvatarFileID),
				Status: run.Status, ErrorCode: run.ErrorCode, LastError: run.LastError,
				Process: support.MapPtr(run.Process, conversationAgentProcessFromAction)}
		}),
		PendingAgents: arr.Map(history.PendingAgents, func(agent processquery.PendingAgent) appservice.ConversationPendingAgent {
			return appservice.ConversationPendingAgent{
				IdentityID: agent.IdentityID, DisplayName: agent.DisplayName, AvatarURL: optionalFileURL(avatarURLs, agent.AvatarFileID),
				PersonalResponsibleName: agent.PersonalResponsibleName,
			}
		}),
	}
	result.Before = encodeConversationMessageCursor(conversationID, history.Before)
	result.After = encodeConversationMessageCursor(conversationID, history.After)
	return result, nil
}

// conversationAvatarURLs 批量解析消息发送者、引用发送者、单聊目标和运行中 Agent 的头像。
func (o *conversationOps) conversationAvatarURLs(ctx context.Context, identity *servermodels.Identity, messages []conversationaction.ConversationMessage, extraFileIDs ...*string) (map[string]string, error) {
	fileIDs := arr.FilterMap(extraFileIDs, func(fileID *string) (string, bool) {
		return support.Deref(fileID), fileID != nil
	})
	for _, message := range messages {
		if message.Sender != nil && message.Sender.AvatarFileID != nil {
			fileIDs = append(fileIDs, *message.Sender.AvatarFileID)
		}
		if message.ReplyTo != nil && message.ReplyTo.Sender != nil && message.ReplyTo.Sender.AvatarFileID != nil {
			fileIDs = append(fileIDs, *message.ReplyTo.Sender.AvatarFileID)
		}
	}
	return o.files.activeFileURLs(ctx, identity, fileIDs)
}

// conversationMessageWithAvatar 转换发送结果并补充头像地址。
func (o *conversationOps) conversationMessageWithAvatar(ctx context.Context, identity *servermodels.Identity, message conversationaction.ConversationMessage) appservice.ConversationMessage {
	urls, err := o.conversationAvatarURLs(ctx, identity, []conversationaction.ConversationMessage{message})
	if err != nil {
		slog.WarnContext(ctx, "读取已保存消息头像失败", "message_id", message.ID, "error", err)
	}
	return conversationMessageFromAction(ctx, message, urls)
}

// conversationLocalAgentTurn 转换运行交给本机 Agent 的一轮。
func conversationLocalAgentTurn(turn processquery.LocalAgentTurn) appservice.ConversationLocalAgentTurn {
	return appservice.ConversationLocalAgentTurn{ToolCallID: turn.ToolCallID, LocalAgent: turn.LocalAgent}
}

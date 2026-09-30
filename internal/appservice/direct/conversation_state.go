//go:build server

// 会话已读、输入状态、未读标记、置顶、归档与通知设置。
package direct

import (
	"context"
	"errors"

	"log/slog"
	"net/http"
	"strconv"

	conversationaction "github.com/runforyou-ai/luway/internal/actions/conversation"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// MarkConversationRead 单调推进当前用户的会话已读水位。
func (o *directOperations) MarkConversationRead(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.MarkConversationReadInput) (appservice.ConversationReadState, error) {
	state, err := o.markConversationRead.Execute(ctx, identity, conversationID, input.LastReadMessageID, input.ClearUnreadMark)
	if err != nil {
		return appservice.ConversationReadState{}, conversationReadError(ctx, meta, err, identity.Organization.ID, conversationID)
	}
	return appservice.ConversationReadState{ReadSeq: strconv.FormatInt(state.ReadSeq, 10), LastReadMessageID: state.LastReadMessageID, LastReadAt: state.LastReadAt}, nil
}

// ReportConversationTyping 按会话类型校验发送资格后发布当前用户的输入状态。
func (o *directOperations) ReportConversationTyping(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ConversationTypingInput) error {
	err := o.reportConversationTyping.Execute(ctx, identity, conversationID, input.Active)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, conversationaction.ErrConversationNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
	}
	slog.Warn("发布会话输入状态失败", "organization_id", identity.Organization.ID, "conversation_id", conversationID, "error", err)
	return appservice.UnavailableError(meta, i18n.ErrorServerUnavailable, nil).WithStatus(http.StatusServiceUnavailable)
}

// UpdateConversationUnreadMark 保存个人未读标记并保留已读和提及查看水位。
func (o *directOperations) UpdateConversationUnreadMark(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ConversationUnreadMarkInput) error {
	if err := o.updateConversationUnreadMark.Execute(ctx, identity, conversationID, input.MarkedUnread); err != nil {
		return conversationReadError(ctx, meta, err, identity.Organization.ID, conversationID)
	}
	if input.MarkedUnread {
		slog.Info("会话已标为未读", "organization_id", identity.Organization.ID, "conversation_id", conversationID, "user_id", identity.User.ID)
	}
	return nil
}

// UpdateConversationPin 保存个人置顶事实与置顶顺序，并返回写入后的顺序版本。
func (o *directOperations) UpdateConversationPin(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ConversationPinInput) (appservice.ConversationPinState, error) {
	expectedVersion, err := strconv.ParseInt(input.ExpectedPinOrderVersion, 10, 64)
	if input.ExpectedPinOrderVersion == "" || err != nil {
		return appservice.ConversationPinState{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed,
			map[string]i18n.Key{"expectedPinOrderVersion": i18n.FieldConversationPinTargetInvalid})
	}
	state, err := o.updateConversationPin.Execute(ctx, identity, conversationaction.ConversationPinInput{
		ConversationID: conversationID, Pinned: input.Pinned, NeighborID: input.NeighborID,
		Position: domain.ConversationPinPosition(input.Position), ExpectedPinOrderVersion: expectedVersion,
	})
	if err != nil {
		return appservice.ConversationPinState{}, conversationPinError(ctx, meta, err, identity.Organization.ID, conversationID)
	}
	slog.Info("会话置顶已保存", "organization_id", identity.Organization.ID, "conversation_id", conversationID, "user_id", identity.User.ID, "pinned", state.Pinned)
	return appservice.ConversationPinState{Pinned: state.Pinned, PinOrderVersion: strconv.FormatInt(state.PinOrderVersion, 10)}, nil
}

// conversationPinError 转换个人置顶写入错误，顺序版本过期与邻居失效都要求客户端整区重读。
func conversationPinError(ctx context.Context, meta appservice.RequestMeta, err error, organizationID, conversationID string) error {
	if mapped := commonActionError(ctx, meta, err); mapped != nil {
		return mapped
	}
	if errors.Is(err, conversationaction.ErrConversationNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
	}
	if validationError, ok := errors.AsType[*conversationaction.ValidationError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, conversationMessageValidationKeys))
	}
	if conflictError, ok := errors.AsType[*conversationaction.ConflictError](err); ok {
		return appservice.ConflictError(meta, i18n.ErrorConversationPinOrderStale, conflictError.Reason)
	}
	slog.Warn("更新会话置顶失败", "organization_id", organizationID, "conversation_id", conversationID, "error", err)
	return appservice.FailedError(meta, i18n.ErrorConversationPinUpdateFailed)
}

// UpdateConversationArchive 保存个人归档状态，归档同时取消置顶。
func (o *directOperations) UpdateConversationArchive(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ConversationArchiveInput) error {
	err := o.updateConversationArchive.Execute(ctx, identity, conversationID, input.Archived)
	if err == nil {
		slog.Info("会话归档状态已保存", "organization_id", identity.Organization.ID, "conversation_id", conversationID, "user_id", identity.User.ID, "archived", input.Archived)
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, identityaction.ErrInvalid) {
		return appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
	}
	if errors.Is(err, conversationaction.ErrConversationNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
	}
	if validationError, ok := errors.AsType[*conversationaction.ValidationError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, conversationMessageValidationKeys))
	}
	slog.Warn("更新会话归档状态失败", "organization_id", identity.Organization.ID, "conversation_id", conversationID, "error", err)
	return appservice.FailedError(meta, i18n.ErrorConversationArchiveUpdateFailed)
}

// UpdateConversationNotificationSettings 保存当前用户的原生会话提醒设置。
func (o *directOperations) UpdateConversationNotificationSettings(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ConversationNotificationSettingsInput) (appservice.ConversationNotificationSettings, error) {
	settings, err := o.updateConversationNotifications.Execute(ctx, identity, conversationID, input.Muted)
	if err != nil {
		return appservice.ConversationNotificationSettings{}, conversationNotificationSettingsError(ctx, meta, err, identity.Organization.ID, conversationID)
	}
	slog.Info("会话提醒设置已保存", "organization_id", identity.Organization.ID, "conversation_id", conversationID, "user_id", identity.User.ID, "muted", settings.Muted)
	return appservice.ConversationNotificationSettings{Muted: settings.Muted}, nil
}

// conversationNotificationSettingsError 转换会话提醒设置更新错误。
func conversationNotificationSettingsError(ctx context.Context, meta appservice.RequestMeta, err error, organizationID, conversationID string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, identityaction.ErrInvalid) {
		return appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
	}
	if errors.Is(err, conversationaction.ErrConversationNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
	}
	if validationError, ok := errors.AsType[*conversationaction.ValidationError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, conversationMessageValidationKeys))
	}
	slog.Warn("更新会话提醒设置失败", "organization_id", organizationID, "conversation_id", conversationID, "error", err)
	return appservice.FailedError(meta, i18n.ErrorConversationNotifyUpdateFailed)
}

// conversationReadError 转换会话阅读状态更新错误。
func conversationReadError(ctx context.Context, meta appservice.RequestMeta, err error, organizationID, conversationID string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, identityaction.ErrInvalid) {
		return appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
	}
	if errors.Is(err, conversationaction.ErrConversationNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
	}
	if validationError, ok := errors.AsType[*conversationaction.ValidationError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, translateValidationFields(validationError.Fields, conversationMessageValidationKeys))
	}
	slog.Warn("更新会话阅读状态失败", "organization_id", organizationID, "conversation_id", conversationID, "error", err)
	return appservice.FailedError(meta, i18n.ErrorConversationReadUpdateFailed)
}

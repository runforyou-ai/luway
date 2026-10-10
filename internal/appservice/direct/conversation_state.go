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
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// MarkConversationRead 单调推进当前用户的会话已读水位。
func (o *conversationOps) MarkConversationRead(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.MarkConversationReadInput) (appservice.ConversationReadState, error) {
	state, err := o.markConversationRead.Execute(ctx, identity, conversationID, input.LastReadMessageID, input.ClearUnreadMark)
	if err != nil {
		return appservice.ConversationReadState{}, conversationReadError(meta, err)
	}
	return appservice.ConversationReadState{ReadSeq: strconv.FormatInt(state.ReadSeq, 10), LastReadMessageID: state.LastReadMessageID, LastReadAt: state.LastReadAt}, nil
}

// ReportConversationTyping 按会话类型校验发送资格后发布当前用户的输入状态。
func (o *conversationOps) ReportConversationTyping(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ConversationTypingInput) error {
	err := o.reportConversationTyping.Execute(ctx, identity, conversationID, input.Active)
	if err == nil {
		return nil
	}
	if errors.Is(err, conversationaction.ErrConversationNotFound) {
		return appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
	}
	slog.WarnContext(ctx, "发布会话输入状态失败", "conversation_id", conversationID, "error", err)
	return appservice.UnavailableError(meta, i18n.ErrorServerUnavailable, nil).WithStatus(http.StatusServiceUnavailable)
}

// UpdateConversationUnreadMark 保存个人未读标记并保留已读和提及查看水位。
func (o *conversationOps) UpdateConversationUnreadMark(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ConversationUnreadMarkInput) error {
	if err := o.updateConversationUnreadMark.Execute(ctx, identity, conversationID, input.MarkedUnread); err != nil {
		return conversationReadError(meta, err)
	}
	if input.MarkedUnread {
		slog.InfoContext(ctx, "会话已标为未读", "conversation_id", conversationID, "user_id", identity.User.ID)
	}
	return nil
}

// UpdateConversationPin 保存个人置顶事实与置顶顺序，并返回写入后的顺序版本。
func (o *conversationOps) UpdateConversationPin(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ConversationPinInput) (appservice.ConversationPinState, error) {
	expectedVersion, err := strconv.ParseInt(input.ExpectedPinOrderVersion, 10, 64)
	if err != nil {
		return appservice.ConversationPinState{}, appservice.InvalidError(meta, i18n.ErrorValidationFailed,
			map[string]i18n.Key{"expectedPinOrderVersion": i18n.FieldConversationPinTargetInvalid})
	}
	state, err := o.updateConversationPin.Execute(ctx, identity, conversationaction.ConversationPinInput{
		ConversationID: conversationID, Pinned: input.Pinned, NeighborID: input.NeighborID,
		Position: input.Position, ExpectedPinOrderVersion: expectedVersion,
	})
	if err != nil {
		return appservice.ConversationPinState{}, conversationPinError(meta, err)
	}
	slog.InfoContext(ctx, "会话置顶已保存", "conversation_id", conversationID, "user_id", identity.User.ID, "pinned", state.Pinned)
	return appservice.ConversationPinState{Pinned: state.Pinned, PinOrderVersion: strconv.FormatInt(state.PinOrderVersion, 10)}, nil
}

// conversationPinErrors 是个人置顶写入的错误转换规则，顺序版本过期与邻居失效都要求客户端整区重读。
var conversationPinErrors = dispatch.Catalogs(dispatch.CommonErrors, dispatch.Catalog{
	dispatch.Is(conversationaction.ErrConversationNotFound, dispatch.NotFound(i18n.ErrorConversationNotFound)),
	dispatch.FieldRule(conversationMessageValidationKeys),
	conversationConflictRule(i18n.ErrorConversationPinOrderStale, nil),
})

// conversationStateErrors 是会话归档、提醒和阅读状态更新的错误转换规则。
var conversationStateErrors = dispatch.Catalog{
	dispatch.SessionRule,
	dispatch.Is(conversationaction.ErrConversationNotFound, dispatch.NotFound(i18n.ErrorConversationNotFound)),
}

// conversationPinError 转换个人置顶写入错误，顺序版本过期与邻居失效都要求客户端整区重读。
func conversationPinError(meta appservice.RequestMeta, err error) error {
	return conversationPinErrors.Translate(meta, err, i18n.ErrorConversationPinUpdateFailed)
}

// UpdateConversationArchive 保存个人归档状态，归档同时取消置顶。
func (o *conversationOps) UpdateConversationArchive(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ConversationArchiveInput) error {
	err := o.updateConversationArchive.Execute(ctx, identity, conversationID, input.Archived)
	if err == nil {
		slog.InfoContext(ctx, "会话归档状态已保存", "conversation_id", conversationID, "user_id", identity.User.ID, "archived", input.Archived)
		return nil
	}
	return conversationStateErrors.Translate(meta, err, i18n.ErrorConversationArchiveUpdateFailed)
}

// UpdateConversationNotificationSettings 保存当前用户的原生会话提醒设置。
func (o *conversationOps) UpdateConversationNotificationSettings(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID string, input appservice.ConversationNotificationSettingsInput) (appservice.ConversationNotificationSettings, error) {
	settings, err := o.updateConversationNotifications.Execute(ctx, identity, conversationID, input.Muted)
	if err != nil {
		return appservice.ConversationNotificationSettings{}, conversationNotificationSettingsError(meta, err)
	}
	slog.InfoContext(ctx, "会话提醒设置已保存", "conversation_id", conversationID, "user_id", identity.User.ID, "muted", settings.Muted)
	return appservice.ConversationNotificationSettings{Muted: settings.Muted}, nil
}

// conversationNotificationSettingsError 转换会话提醒设置更新错误。
func conversationNotificationSettingsError(meta appservice.RequestMeta, err error) error {
	return conversationStateErrors.Translate(meta, err, i18n.ErrorConversationNotificationUpdateFailed)
}

// conversationReadError 转换会话阅读状态更新错误。
func conversationReadError(meta appservice.RequestMeta, err error) error {
	return conversationStateErrors.Translate(meta, err, i18n.ErrorConversationReadUpdateFailed)
}

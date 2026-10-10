//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"
	"strconv"

	channelaction "github.com/runforyou-ai/luway/internal/actions/channel"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	telegramaction "github.com/runforyou-ai/luway/internal/actions/telegram"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/telegram"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	"github.com/runforyou-ai/luway/pkg/connectiontest"
	"github.com/runforyou-ai/support"
	"github.com/uptrace/bun"
)

// telegramOps 持有 Telegram 渠道连接配置的 Action 和 Query。
type telegramOps struct {
	getTelegramChannel              *telegramaction.GetChannelQuery
	testTelegramConnection          *telegramaction.TestConnectionAction
	saveTelegramConnection          *telegramaction.SaveConnectionAction
	regenerateTelegramGatewaySecret *telegramaction.RegenerateGatewaySecretAction
}

// newTelegramOps 创建 Telegram 渠道连接配置的业务实现依赖。
func newTelegramOps(db *bun.DB, connectionRunner *connectiontest.Runner, telegramAPI *telegram.Client) *telegramOps {
	return &telegramOps{
		getTelegramChannel:              telegramaction.NewGetChannelQuery(db),
		testTelegramConnection:          telegramaction.NewTestConnectionAction(db, connectionRunner, telegramAPI),
		saveTelegramConnection:          telegramaction.NewSaveConnectionAction(db, connectionRunner, telegramAPI),
		regenerateTelegramGatewaySecret: telegramaction.NewRegenerateGatewaySecretAction(db),
	}
}

const (
	telegramBotReuseConfirmationReason = "telegram_bot_reuse_confirmation_required"
	telegramGatewayModeRequiredReason  = "telegram_gateway_mode_required"
)

// GetTelegramChannel 返回 Telegram 渠道详情。
func (o *telegramOps) GetTelegramChannel(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string) (appservice.TelegramChannel, error) {
	detail, err := o.getTelegramChannel.Execute(ctx, identity, channelID)
	if err != nil {
		return appservice.TelegramChannel{}, channelError(meta, err, i18n.ErrorChannelReadFailed)
	}
	return telegramChannelFromRecord(detail), nil
}

// TestTelegramChannelConnection 测试 Telegram 草稿 Token。
func (o *telegramOps) TestTelegramChannelConnection(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string, input appservice.TelegramChannelConnectionTestInput) error {
	err := o.testTelegramConnection.Execute(ctx, identity, channelID, telegramaction.ConnectionTestInput{BotToken: input.BotToken})
	if err == nil {
		return nil
	}
	return telegramConnectionError(meta, err, i18n.ErrorTelegramConnectionTestFailed)
}

// SaveTelegramChannelConnection 保存 Telegram 机器人和 Webhook 设置。
func (o *telegramOps) SaveTelegramChannelConnection(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string, input appservice.TelegramChannelConnectionInput) (appservice.TelegramChannel, error) {
	detail, err := o.saveTelegramConnection.Execute(ctx, identity, channelID, telegramaction.ConnectionInput{
		ConnectionMode: input.ConnectionMode,
		BotToken:       input.BotToken, WebhookBaseURL: input.WebhookBaseURL, ConfirmBotReuse: input.ConfirmBotReuse,
	})
	if err != nil {
		return appservice.TelegramChannel{}, telegramConnectionError(meta, err, i18n.ErrorTelegramConnectionSaveFailed)
	}
	slog.InfoContext(ctx, "Telegram 渠道连接已保存", "channel_id", channelID)
	return telegramChannelFromRecord(detail), nil
}

// RegenerateTelegramGatewaySecret 重新生成业务系统转发 Telegram 消息使用的转发密钥。
func (o *telegramOps) RegenerateTelegramGatewaySecret(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, channelID string) (appservice.TelegramChannel, error) {
	detail, err := o.regenerateTelegramGatewaySecret.Execute(ctx, identity, channelID)
	if errors.Is(err, telegramaction.ErrGatewayModeRequired) {
		return appservice.TelegramChannel{}, appservice.ConflictError(meta, i18n.ErrorTelegramGatewayModeRequired, telegramGatewayModeRequiredReason)
	}
	if err != nil {
		return appservice.TelegramChannel{}, channelError(meta, err, i18n.ErrorTelegramGatewaySecretRegenerateFailed)
	}
	slog.InfoContext(ctx, "Telegram 渠道转发密钥已重新生成", "channel_id", channelID)
	return telegramChannelFromRecord(detail), nil
}

// telegramChannelFromRecord 转换 Telegram 渠道详情。
func telegramChannelFromRecord(detail *telegramaction.ChannelDetail) appservice.TelegramChannel {
	connection := detail.Connection
	botID := support.MapPtr(connection.BotID, func(id int64) string { return strconv.FormatInt(id, 10) })
	status := support.MapPtr(connection.WebhookStatus, func(status string) appservice.TelegramWebhookStatus { return appservice.TelegramWebhookStatus(status) })
	return appservice.TelegramChannel{
		MessageChannelSummary: messageChannelFromRecord(&detail.MessageChannelRecord),
		Connection: appservice.TelegramChannelConnection{
			ConnectionMode: appservice.TelegramConnectionMode(connection.ConnectionMode),
			BotToken:       connection.BotToken, BotID: botID, BotUsername: connection.BotUsername,
			BotDisplayName: connection.BotDisplayName, WebhookURL: connection.WebhookURL,
			WebhookSecret: connection.WebhookSecret, WebhookStatus: status,
		},
	}
}

// telegramConnectionError 转换 Telegram 连接校验和外部访问错误。
func telegramConnectionError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	if validationError, ok := errors.AsType[*common.FieldError](err); ok {
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, dispatch.TranslateFields(validationError.Fields, telegramFieldKeys))
	}
	if errors.Is(err, channelaction.ErrNotFound) || errors.Is(err, identityaction.ErrInvalid) {
		return channelError(meta, err, failureKey)
	}
	if errors.Is(err, telegramaction.ErrBotReuseConfirmationRequired) {
		return appservice.ConflictError(meta, i18n.FieldTelegramBotInUse, telegramBotReuseConfirmationReason)
	}
	_, kind, classified := connectiontest.Details(err)
	if !classified {
		return channelError(meta, err, failureKey)
	}
	switch kind {
	case connectiontest.FailureInvalidConfig, connectiontest.FailureUnauthorized, connectiontest.FailureForbidden, connectiontest.FailureNotFound:
		return appservice.InvalidError(meta, i18n.ErrorValidationFailed, map[string]i18n.Key{"botToken": i18n.FieldTelegramBotTokenInvalid})
	default:
		return appservice.UnavailableError(meta, failureKey, nil)
	}
}

// telegramFieldKeys 把 Telegram 连接校验错误码映射为本地化文案键。
var telegramFieldKeys = map[common.FieldCode]i18n.Key{
	telegramaction.ValidationBaseURLInvalid: i18n.FieldTelegramWebhookBaseURLInvalid,
}

//go:build server

package direct

import (
	"context"

	deliveryaction "github.com/runforyou-ai/luway/internal/actions/channeldelivery"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/dispatch"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// ResolveChannelMessageDelivery 按成员选择的处理方式处理失败或待确认的投递。
func (o *inboxOps) ResolveChannelMessageDelivery(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID, deliveryID string, input appservice.ChannelDeliveryResolveInput) error {
	return channelDeliveryError(meta, o.customerDeliveries.Resolve(ctx, identity, conversationID, deliveryID, input.Resolution, input.ConfirmDuplicateRisk))
}

// channelDeliveryErrors 是投递管理的错误转换规则。
var channelDeliveryErrors = dispatch.Catalog{
	dispatch.SessionRule,
	dispatch.Is(deliveryaction.ErrUnavailable, dispatch.NotFound(i18n.ErrorConversationNotFound)),
	dispatch.Is(deliveryaction.ErrConflict, dispatch.Conflict(i18n.ErrorChannelDeliveryConflict, "delivery_state_conflict")),
}

// channelDeliveryError 转换投递管理错误并保留会话恢复语义。
func channelDeliveryError(meta appservice.RequestMeta, err error) error {
	return channelDeliveryErrors.Translate(meta, err, i18n.ErrorChannelDeliveryFailed)
}

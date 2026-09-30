//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	deliveryaction "github.com/runforyou-ai/luway/internal/actions/customerdelivery"
	identityaction "github.com/runforyou-ai/luway/internal/actions/identity"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/luway/internal/i18n"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
)

// ResolveCustomerMessageDelivery 重新校验投递处理意图。
func (o *directOperations) ResolveCustomerMessageDelivery(ctx context.Context, meta appservice.RequestMeta, identity *servermodels.Identity, conversationID, deliveryID string, input appservice.CustomerDeliveryResolveInput) error {
	if !common.ValidUUID(conversationID) || !common.ValidUUID(deliveryID) {
		return appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
	}
	return customerDeliveryError(meta, o.customerDeliveries.Resolve(ctx, identity, conversationID, deliveryID, domain.CustomerDeliveryResolution(input.Resolution), input.ConfirmDuplicateRisk))
}

// customerDeliveryError 转换投递管理错误并保留会话恢复语义。
func customerDeliveryError(meta appservice.RequestMeta, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, identityaction.ErrInvalid) {
		return appservice.SessionError(meta, appservice.SessionStateLogin, i18n.ErrorAuthenticationRequired)
	}
	if errors.Is(err, deliveryaction.ErrUnavailable) {
		return appservice.NotFoundError(meta, i18n.ErrorConversationNotFound)
	}
	if errors.Is(err, deliveryaction.ErrConflict) {
		return appservice.ConflictError(meta, i18n.ErrorCustomerDeliveryConflict, "delivery_state_conflict")
	}
	slog.Warn("客户消息投递操作失败", "error", err)
	return appservice.FailedError(meta, i18n.ErrorCustomerDeliveryFailed)
}

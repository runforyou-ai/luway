//go:build server

package direct

import (
	"context"
	"errors"
	"log/slog"

	commerceaction "github.com/runforyou-ai/luway/internal/actions/commerce"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/runforyou-ai/luway/internal/integration/commerce"
	servermodels "github.com/runforyou-ai/luway/internal/storage/server/models"
	servertask "github.com/runforyou-ai/luway/internal/task/server"
	"github.com/uptrace/bun"
)

// commerceOps 持有商业服务配对与变更同步的 Action 和 Query。
type commerceOps struct {
	commercePairingRead *commerceaction.PairingQuery
	pairCommerce        *commerceaction.PairAction
	unpairCommerce      *commerceaction.UnpairAction
	syncCommerce        *commerceaction.SyncChangesAction
}

// newCommerceOps 创建商业服务的业务实现依赖，client 用服务器密钥签名请求商业服务。
func newCommerceOps(db *bun.DB, client *commerce.Client, taskEnqueuer servertask.TxEnqueuer) commerceOps {
	return commerceOps{
		commercePairingRead: commerceaction.NewPairingQuery(db),
		pairCommerce:        commerceaction.NewPairAction(db, client, taskEnqueuer),
		unpairCommerce:      commerceaction.NewUnpairAction(db),
		syncCommerce:        commerceaction.NewSyncChangesAction(db, client),
	}
}

// GetCommercePairing 返回平台与商业服务的配对状态。
func (o *directOperations) GetCommercePairing(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.CommercePairing, error) {
	pairing, err := o.commercePairingRead.Execute(ctx)
	if err != nil {
		return appservice.CommercePairing{}, commerceError(meta, err, i18n.ErrorCommerceReadFailed)
	}
	return commercePairingFromAction(pairing), nil
}

// PairCommerce 用商业服务生成的配对码完成配对，替换现有配对并从头读取商业服务变更。
func (o *directOperations) PairCommerce(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity, input appservice.PairCommerceInput) (appservice.CommercePairing, error) {
	pairing, err := o.pairCommerce.Execute(ctx, account, input.PairingCode)
	if err != nil {
		return appservice.CommercePairing{}, commerceError(meta, err, i18n.ErrorCommercePairFailed)
	}
	slog.Info("商业服务已配对", "account_id", account.Account.ID, "url", pairing.URL, "service_id", pairing.ServiceID)
	return commercePairingFromAction(&pairing), nil
}

// UnpairCommerce 解除与商业服务的配对并删除已应用的工作区权益。
func (o *directOperations) UnpairCommerce(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) error {
	if err := o.unpairCommerce.Execute(ctx, account); err != nil {
		return commerceError(meta, err, i18n.ErrorCommerceUnpairFailed)
	}
	slog.Info("商业服务已解除配对", "account_id", account.Account.ID)
	return nil
}

// SyncCommerce 立即读取商业服务的变更。
func (o *directOperations) SyncCommerce(ctx context.Context, meta appservice.RequestMeta, account *servermodels.AccountIdentity) (appservice.CommercePairing, error) {
	if err := o.syncCommerce.Sync(ctx); err != nil {
		return appservice.CommercePairing{}, commerceError(meta, err, i18n.ErrorCommerceSyncFailed)
	}
	pairing, err := o.commercePairingRead.Execute(ctx)
	if err != nil {
		return appservice.CommercePairing{}, commerceError(meta, err, i18n.ErrorCommerceReadFailed)
	}
	return commercePairingFromAction(pairing), nil
}

// commerceError 把商业服务配对与同步错误转换为本地化错误，其余错误按平台管理错误处理。
func commerceError(meta appservice.RequestMeta, err error, failureKey i18n.Key) error {
	// 把配对码、商业服务拒绝与通信错误映射为本地化文案键。
	for target, key := range map[error]i18n.Key{
		commerceaction.ErrPairingCodeInvalid: i18n.ErrorCommercePairingCodeInvalid,
		commerceaction.ErrPairingRejected:    i18n.ErrorCommercePairingRejected,
		commerceaction.ErrUnavailable:        i18n.ErrorCommerceUnavailable,
		commerceaction.ErrNotPaired:          i18n.ErrorCommerceNotPaired,
		commerceaction.ErrInvalidData:        i18n.ErrorCommerceInvalidData,
	} {
		if errors.Is(err, target) {
			return appservice.InvalidError(meta, key, nil)
		}
	}
	return platformError(meta, err, failureKey)
}

// commercePairingFromAction 把配对状态转换为应用契约，未配对时只返回 Paired 为 false。
func commercePairingFromAction(pairing *commerceaction.Pairing) appservice.CommercePairing {
	if pairing == nil {
		return appservice.CommercePairing{}
	}
	output := appservice.CommercePairing{
		Paired: true, URL: pairing.URL, ServiceID: pairing.ServiceID, PairedAt: &pairing.PairedAt,
		SyncedAt: pairing.SyncedAt, FailedAt: pairing.FailedAt,
	}
	if pairing.Failure != nil {
		failure := appservice.CommerceSyncFailure(*pairing.Failure)
		output.Failure = &failure
	}
	return output
}

package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// CommerceSyncFailure 表示最近一次读取商业服务变更失败的原因。
type CommerceSyncFailure string

const (
	CommerceSyncFailureUnavailable CommerceSyncFailure = CommerceSyncFailure(domain.CommerceSyncFailureUnavailable)
	CommerceSyncFailureNotPaired   CommerceSyncFailure = CommerceSyncFailure(domain.CommerceSyncFailureNotPaired)
	CommerceSyncFailureInvalidData CommerceSyncFailure = CommerceSyncFailure(domain.CommerceSyncFailureInvalidData)
	CommerceSyncFailureFailed      CommerceSyncFailure = CommerceSyncFailure(domain.CommerceSyncFailureFailed)
)

// CommercePairing 定义平台与商业服务的配对状态；未配对时只有 Paired 为 false，最近一次读取成功时 Failure 为空。
type CommercePairing struct {
	Paired    bool                 `json:"paired"`
	URL       string               `json:"url"`
	ServiceID string               `json:"serviceId"`
	PairedAt  *time.Time           `json:"pairedAt"`
	SyncedAt  *time.Time           `json:"syncedAt"`
	FailedAt  *time.Time           `json:"failedAt"`
	Failure   *CommerceSyncFailure `json:"failure"`
}

// PairCommerceInput 定义平台管理员粘贴的商业服务配对码。
type PairCommerceInput struct {
	PairingCode string `json:"pairingCode"`
}

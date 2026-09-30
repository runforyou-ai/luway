package appservice

import "github.com/runforyou-ai/luway/internal/domain"

// CustomerDeliveryStatus 表示客户消息的外部投递状态。
type CustomerDeliveryStatus domain.CustomerDeliveryStatus

// CustomerDeliveryResolution 表示人工投递处理操作。
type CustomerDeliveryResolution domain.CustomerDeliveryResolution

// CustomerDeliveryResolveInput 定义人工确认或重试意图。
type CustomerDeliveryResolveInput struct {
	Resolution           CustomerDeliveryResolution `json:"resolution"`
	ConfirmDuplicateRisk bool                       `json:"confirmDuplicateRisk"`
}

// CustomerMessageDelivery 定义成员可见的外部投递结果。
type CustomerMessageDelivery struct {
	CanRetry  bool                   `json:"canRetry"`
	Paused    bool                   `json:"paused"`
	ID        string                 `json:"id"`
	Status    CustomerDeliveryStatus `json:"status"`
	LastError string                 `json:"lastError"`
}

const (
	CustomerDeliveryPending     CustomerDeliveryStatus = CustomerDeliveryStatus(domain.CustomerDeliveryPending)
	CustomerDeliverySending     CustomerDeliveryStatus = CustomerDeliveryStatus(domain.CustomerDeliverySending)
	CustomerDeliverySent        CustomerDeliveryStatus = CustomerDeliveryStatus(domain.CustomerDeliverySent)
	CustomerDeliveryRetryWait   CustomerDeliveryStatus = CustomerDeliveryStatus(domain.CustomerDeliveryRetryWait)
	CustomerDeliveryFailed      CustomerDeliveryStatus = CustomerDeliveryStatus(domain.CustomerDeliveryFailed)
	CustomerDeliveryUncertain   CustomerDeliveryStatus = CustomerDeliveryStatus(domain.CustomerDeliveryUncertain)
	CustomerDeliveryNeedsReview CustomerDeliveryStatus = CustomerDeliveryStatus(domain.CustomerDeliveryNeedsReview)
)
const (
	CustomerDeliveryRetry         CustomerDeliveryResolution = CustomerDeliveryResolution(domain.CustomerDeliveryRetry)
	CustomerDeliveryConfirmSent   CustomerDeliveryResolution = CustomerDeliveryResolution(domain.CustomerDeliveryConfirmSent)
	CustomerDeliveryConfirmFailed CustomerDeliveryResolution = CustomerDeliveryResolution(domain.CustomerDeliveryConfirmFailed)
)

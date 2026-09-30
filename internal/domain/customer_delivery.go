package domain

// CustomerDeliveryStatus 表示外部客户消息的投递状态。
type CustomerDeliveryStatus string

const (
	CustomerDeliveryPending     CustomerDeliveryStatus = "pending"
	CustomerDeliverySending     CustomerDeliveryStatus = "sending"
	CustomerDeliverySent        CustomerDeliveryStatus = "sent"
	CustomerDeliveryRetryWait   CustomerDeliveryStatus = "retry_wait"
	CustomerDeliveryFailed      CustomerDeliveryStatus = "failed"
	CustomerDeliveryUncertain   CustomerDeliveryStatus = "uncertain"
	CustomerDeliveryNeedsReview CustomerDeliveryStatus = "needs_review"
)

// CustomerDeliveryResolution 表示人工处理投递结果的操作。
type CustomerDeliveryResolution string

const (
	CustomerDeliveryRetry         CustomerDeliveryResolution = "retry"
	CustomerDeliveryConfirmSent   CustomerDeliveryResolution = "confirm_sent"
	CustomerDeliveryConfirmFailed CustomerDeliveryResolution = "confirm_failed"
)

package domain

// ChannelDeliveryStatus 表示外部渠道消息的投递状态。
type ChannelDeliveryStatus string

const (
	ChannelDeliveryPending     ChannelDeliveryStatus = "pending"
	ChannelDeliverySending     ChannelDeliveryStatus = "sending"
	ChannelDeliverySent        ChannelDeliveryStatus = "sent"
	ChannelDeliveryRetryWait   ChannelDeliveryStatus = "retry_wait"
	ChannelDeliveryFailed      ChannelDeliveryStatus = "failed"
	ChannelDeliveryUncertain   ChannelDeliveryStatus = "uncertain"
	ChannelDeliveryNeedsReview ChannelDeliveryStatus = "needs_review"
)

// ChannelDeliveryResolution 表示人工处理投递结果的操作。
type ChannelDeliveryResolution string

const (
	ChannelDeliveryRetry         ChannelDeliveryResolution = "retry"
	ChannelDeliveryConfirmSent   ChannelDeliveryResolution = "confirm_sent"
	ChannelDeliveryConfirmFailed ChannelDeliveryResolution = "confirm_failed"
)

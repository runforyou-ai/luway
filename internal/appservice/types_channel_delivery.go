package appservice

import "github.com/runforyou-ai/luway/internal/domain"

// ChannelDeliveryStatus 表示渠道消息的外部投递状态。
type ChannelDeliveryStatus = domain.ChannelDeliveryStatus

// ChannelDeliveryResolution 表示人工投递处理操作。
type ChannelDeliveryResolution = domain.ChannelDeliveryResolution

// ChannelDeliveryResolveInput 定义人工确认或重试意图。
type ChannelDeliveryResolveInput struct {
	Resolution           ChannelDeliveryResolution `json:"resolution"`
	ConfirmDuplicateRisk bool                      `json:"confirmDuplicateRisk"`
}

// ChannelMessageDelivery 定义成员可见的外部投递结果。
type ChannelMessageDelivery struct {
	CanRetry  bool                  `json:"canRetry"`
	Paused    bool                  `json:"paused"`
	ID        string                `json:"id"`
	Status    ChannelDeliveryStatus `json:"status"`
	LastError string                `json:"lastError"`
	// PartiallySent 表示消息已有部分内容发出、其余部分尚未发出。
	PartiallySent bool `json:"partiallySent"`
}

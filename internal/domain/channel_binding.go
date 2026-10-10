package domain

import "time"

// 渠道绑定链接的有效期与同一渠道身份两次发送绑定链接的最短间隔。
const (
	ChannelBindingValidity     = 15 * time.Minute
	ChannelBindingLinkInterval = 10 * time.Minute
)

// ChannelBindingStatus 定义渠道绑定链接的状态。
type ChannelBindingStatus string

const (
	ChannelBindingStatusPending ChannelBindingStatus = "pending"
	ChannelBindingStatusExpired ChannelBindingStatus = "expired"
	ChannelBindingStatusUsed    ChannelBindingStatus = "used"
)

//go:build server

package channel

import "errors"

var (
	// ErrNotFound 表示当前企业中不存在指定消息渠道。
	ErrNotFound = errors.New("message channel not found")
	// ErrWeComBotInUse 表示该企业微信机器人已由其他渠道连接；同一机器人同一时刻只能保持一条长连接。
	ErrWeComBotInUse = errors.New("WeCom bot already connected by another channel")
	// ErrWechatPlatformRequired 表示部署尚未配置微信第三方平台，不能创建授权接入公众号渠道。
	ErrWechatPlatformRequired = errors.New("wechat platform required")
	// ErrWechatAccountEnabledElsewhere 表示同一公众号已有另一种接入方式的渠道处于启用状态。
	ErrWechatAccountEnabledElsewhere = errors.New("wechat account enabled in another channel")
)

// WechatEnabledAccountIndex 是保证同一公众号 AppID 在密钥接入与授权接入渠道中最多一个启用的唯一索引。
const WechatEnabledAccountIndex = "channels_wechat_enabled_app_id_unique"

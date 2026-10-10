package appservice

import "context"

// WechatBackend 定义微信渠道与微信开放平台的业务调用。
type WechatBackend interface {
	// GetWechatKeyChannel 返回密钥接入公众号渠道详情、连接与接口调用凭据状态。
	//appservice:route GET /channels/{channelID:uuid}/wechat-key perm=customer_service.manage
	GetWechatKeyChannel(context.Context, RequestMeta, string) (WechatKeyChannel, error)
	// SaveWechatKeyChannelConnection 连接公众号或更新密钥接入凭据，凭据通过微信验证后才保存。
	//appservice:route PUT /channels/{channelID:uuid}/wechat-key/connection perm=customer_service.manage
	SaveWechatKeyChannelConnection(context.Context, RequestMeta, string, WechatKeyConnectionInput) (WechatKeyChannel, error)
	// CheckWechatKeyChannelConnection 立即重新获取密钥接入公众号的接口调用凭据并返回渠道详情。
	//appservice:route POST /channels/{channelID:uuid}/wechat-key/connection/check perm=customer_service.manage
	CheckWechatKeyChannelConnection(context.Context, RequestMeta, string) (WechatKeyChannel, error)
	// GetWechatAuthorizationChannel 返回授权接入公众号渠道详情、授权状态与接口调用凭据状态。
	//appservice:route GET /channels/{channelID:uuid}/wechat-authorization perm=customer_service.manage
	GetWechatAuthorizationChannel(context.Context, RequestMeta, string) (WechatAuthorizationChannel, error)
	// StartWechatAuthorization 为授权接入渠道发起公众号授权，返回在浏览器中打开的授权发起页地址。
	//appservice:route POST /channels/{channelID:uuid}/wechat-authorization/authorization perm=customer_service.manage
	StartWechatAuthorization(context.Context, RequestMeta, string) (WechatAuthorizationStart, error)
	// CheckWechatAuthorizationChannelConnection 立即重新获取授权接入公众号的接口调用凭据并返回渠道详情。
	//appservice:route POST /channels/{channelID:uuid}/wechat-authorization/connection/check perm=customer_service.manage
	CheckWechatAuthorizationChannelConnection(context.Context, RequestMeta, string) (WechatAuthorizationChannel, error)
	// GetWechatPlatform 返回微信第三方平台配置、可用状态、接入地址、各服务器出口 IP 与平台凭据状态。
	//appservice:route GET /platform/wechat auth=admin
	GetWechatPlatform(context.Context, RequestMeta) (WechatPlatform, error)
	// SaveWechatPlatform 保存微信第三方平台凭据，已收到验证票据时立即获取平台凭据；更换 Component AppID 时清空验证票据。
	//appservice:route PUT /platform/wechat auth=admin
	SaveWechatPlatform(context.Context, RequestMeta, WechatPlatformInput) (WechatPlatform, error)
	// CheckWechatPlatform 立即重新获取微信平台凭据并返回结果。
	//appservice:route POST /platform/wechat/check auth=admin
	CheckWechatPlatform(context.Context, RequestMeta) (WechatPlatform, error)
}

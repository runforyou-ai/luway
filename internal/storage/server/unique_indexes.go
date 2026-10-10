//go:build server

package server

// 以下是业务写入按名称识别冲突的唯一索引。
const (
	// UniqueChannelIdentityExternal 是渠道内外部用户的渠道身份唯一索引。
	UniqueChannelIdentityExternal = "channel_identities_channel_external_unique"
	// UniqueContactExternalUser 是工作区内企业用户编号的联系人唯一索引。
	UniqueContactExternalUser = "contacts_workspace_external_user_unique"
	// UniqueServiceSessionOpen 是服务会话未结束周期唯一索引。
	UniqueServiceSessionOpen = "service_sessions_workspace_service_conversation_open_unique"
	// UniqueServiceSessionSequence 是服务会话周期序号唯一索引。
	UniqueServiceSessionSequence = "service_sessions_conversation_sequence_unique"
	// UniqueDirectConversationPair 是规范化单聊身份对唯一索引。
	UniqueDirectConversationPair = "direct_conversations_workspace_identity_pair_unique"
)

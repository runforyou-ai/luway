/** 企业内部群聊的创建、资料、成员与发送调用。 */
import * as ops from "@/api/generated/operations"

/** 创建企业内部群聊。 */
export const createGroupConversation = ops.createGroupConversation

/** 读取企业内部群聊资料和当前成员。 */
export const getGroupConversation = ops.getGroupConversation

/** 修改企业内部群聊资料。 */
export const updateGroupConversation = ops.updateGroupConversation

/** 批量增加企业内部群聊成员。 */
export const addGroupConversationMembers = ops.addGroupConversationMembers

/** 移除企业内部群聊成员。 */
export const removeGroupConversationMember = ops.removeGroupConversationMember

/** 转让企业内部群聊群主。 */
export const transferGroupConversationOwner = ops.transferGroupConversationOwner

/** 退出企业内部群聊。 */
export const leaveGroupConversation = ops.leaveGroupConversation

/** 解散企业内部群聊。 */
export const dissolveGroupConversation = ops.dissolveGroupConversation

/** 发送企业内部群聊文本消息。 */
export const sendGroupTextMessage = ops.sendGroupTextMessage

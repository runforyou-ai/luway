/** 企业内部群聊的创建、资料、成员与发送调用。 */
import {
  AddGroupConversationMembers,
  CreateGroupConversation,
  DissolveGroupConversation,
  GetGroupConversation,
  LeaveGroupConversation,
  RemoveGroupConversationMember,
  SendGroupTextMessage,
  TransferGroupConversationOwner,
  UpdateGroupConversation,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import type { GroupConversation } from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import { bind } from "@/api/client"
import type { NonNullArrays } from "@/api/normalize"

export type GroupConversationData = NonNullArrays<GroupConversation>

/** 创建企业内部群聊。 */
export const createGroupConversation = bind(CreateGroupConversation)

/** 读取企业内部群聊资料和当前成员。 */
export const getGroupConversation = bind(GetGroupConversation)

/** 修改企业内部群聊资料。 */
export const updateGroupConversation = bind(UpdateGroupConversation)

/** 批量增加企业内部群聊成员。 */
export const addGroupConversationMembers = bind(AddGroupConversationMembers)

/** 移除企业内部群聊成员。 */
export const removeGroupConversationMember = bind(RemoveGroupConversationMember)

/** 转让企业内部群聊群主。 */
export const transferGroupConversationOwner = bind(TransferGroupConversationOwner)

/** 退出企业内部群聊。 */
export const leaveGroupConversation = bind(LeaveGroupConversation)

/** 解散企业内部群聊。 */
export const dissolveGroupConversation = bind(DissolveGroupConversation)

/** 发送企业内部群聊文本消息。 */
export const sendGroupTextMessage = bind(SendGroupTextMessage)

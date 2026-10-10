/** 登录账号、工作区列表与创建调用。 */
import * as ops from "@/api/generated/operations"

/** 读取当前登录账号。 */
export const loadAccount = ops.loadAccount

/** 读取当前账号作为有效成员可进入的工作区。 */
export const listWorkspaces = ops.listWorkspaces

/** 读取当前账号在各工作区的提醒数量。 */
export const listWorkspaceAttention = ops.listWorkspaceAttention

/** 创建工作区，当前账号成为首位管理员。 */
export const createWorkspace = ops.createWorkspace

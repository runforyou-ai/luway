/** 工作区席位调用：席位上限与启用的成员数。 */
import * as ops from "@/api/generated/operations"

/** 读取当前工作区的席位上限与启用的成员数，上限为 0 表示不限。 */
export const getWorkspaceSeats = ops.getWorkspaceSeats

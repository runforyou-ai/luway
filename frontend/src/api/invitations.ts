/** 工作区成员邀请调用。 */
import * as ops from "@/api/generated/operations"

/** 读取当前工作区待接受的邀请。 */
export const listInvitations = ops.listInvitations

/** 邀请账号加入当前工作区，返回只展示一次的邀请链接。 */
export const createInvitation = ops.createInvitation

/** 撤销原邀请并以相同内容重新生成邀请链接。 */
export const regenerateInvitation = ops.regenerateInvitation

/** 撤销待接受的邀请。 */
export const revokeInvitation = ops.revokeInvitation

/** 按邀请令牌读取工作区名称、邀请人和掩码后的受邀邮箱。 */
export const previewInvitation = ops.previewInvitation

/** 由当前账号接受邀请并加入工作区。 */
export const acceptInvitation = ops.acceptInvitation

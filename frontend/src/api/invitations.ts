/** 工作区成员邀请调用。 */
import {
  AcceptInvitation,
  CreateInvitation,
  ListInvitations,
  PreviewInvitation,
  RegenerateInvitation,
  RevokeInvitation,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import { bind } from "@/api/client"

/** 读取当前工作区待接受的邀请。 */
export const listInvitations = bind(ListInvitations)

/** 邀请账号加入当前工作区，返回只展示一次的邀请链接。 */
export const createInvitation = bind(CreateInvitation)

/** 撤销原邀请并以相同内容重新生成邀请链接。 */
export const regenerateInvitation = bind(RegenerateInvitation)

/** 撤销待接受的邀请。 */
export const revokeInvitation = bind(RevokeInvitation)

/** 按邀请令牌读取工作区名称、邀请人和掩码后的受邀邮箱。 */
export const previewInvitation = bind(PreviewInvitation)

/** 由当前账号接受邀请并加入工作区。 */
export const acceptInvitation = bind(AcceptInvitation)

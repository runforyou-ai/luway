/** 微信开放平台调用：部署级第三方平台配置、接入信息与平台凭据状态的读取、保存和重新检测。 */
import * as ops from "@/api/generated/operations"

/** 读取微信第三方平台配置、接入信息、各服务器出口 IP 与平台凭据状态。 */
export const getWechatPlatform = ops.getWechatPlatform

/** 保存微信第三方平台凭据，已收到验证票据时立即获取平台凭据。 */
export const saveWechatPlatform = ops.saveWechatPlatform

/** 立即重新获取微信平台凭据。 */
export const checkWechatPlatform = ops.checkWechatPlatform

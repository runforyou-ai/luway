/** 企业设置与个人账号设置调用。 */
import type { UserPreferencesInput } from "@/api/generated/contract"
import * as ops from "@/api/generated/operations"
import { syncLocale } from "@/platform/system"

/** 修改当前企业通用设置。 */
export const updateWorkspace = ops.updateWorkspace

/** 读取当前企业的客服工作时间。 */
export const getBusinessHours = ops.getBusinessHours

/** 修改当前企业的客服工作时间。 */
export const updateBusinessHours = ops.updateBusinessHours

/** 读取当前企业的客服超时时长。 */
export const getServiceTimeouts = ops.getServiceTimeouts

/** 修改当前企业的客服超时时长。 */
export const updateServiceTimeouts = ops.updateServiceTimeouts

/** 读取当前企业的会话小结设置。 */
export const getServiceSummarySettings = ops.getServiceSummarySettings

/** 修改当前企业的会话小结设置。 */
export const updateServiceSummarySettings = ops.updateServiceSummarySettings

/** 读取当前企业的翻译设置。 */
export const getTranslationSettings = ops.getTranslationSettings

/** 修改当前企业的翻译设置。 */
export const updateTranslationSettings = ops.updateTranslationSettings

/** 读取当前企业的客户身份密钥，未生成时为空。 */
export const getCustomerIdentitySecret = ops.getCustomerIdentitySecret

/** 生成或重新生成当前企业的客户身份密钥，旧密钥立即失效。 */
export const regenerateCustomerIdentitySecret = ops.regenerateCustomerIdentitySecret

/** 读取当前企业的咨询分类目录。 */
export const listServiceCategories = ops.listServiceCategories

/** 新增咨询分类。 */
export const createServiceCategory = ops.createServiceCategory

/** 修改咨询分类。 */
export const updateServiceCategory = ops.updateServiceCategory

/** 删除咨询分类。 */
export const deleteServiceCategory = ops.deleteServiceCategory

/** 修改当前用户的头像、姓名和邮箱。 */
export const updateProfile = ops.updateProfile

/** 修改当前用户的登录密码。 */
export const changePassword = ops.changePassword

/** 修改当前用户偏好设置，原生端同步成员语言。 */
export async function updateUserPreferences(input: UserPreferencesInput) {
  const user = await ops.updateUserPreferences(input)
  syncLocale(user.locale)
  return user
}

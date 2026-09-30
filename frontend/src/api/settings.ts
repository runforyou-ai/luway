/** 企业设置与个人账号设置调用。 */
import {
  ChangePassword,
  CreateServiceCategory,
  DeleteServiceCategory,
  GetBusinessHours,
  GetCustomerIdentitySecret,
  GetServiceSummarySettings,
  GetServiceTimeouts,
  GetTranslationSettings,
  ListServiceCategories,
  RegenerateCustomerIdentitySecret,
  SelectImage,
  UpdateBusinessHours,
  UpdateOrganization,
  UpdateServiceCategory,
  UpdateServiceSummarySettings,
  UpdateServiceTimeouts,
  UpdateTranslationSettings,
  UpdateProfile,
  UpdateUserPreferences,
} from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/service"
import type { BusinessHours } from "../../bindings/github.com/runforyou-ai/cervi/internal/appservice/models"
import { bind } from "@/api/client"
import type { NonNullArrays } from "@/api/normalize"

export type BusinessHoursData = NonNullArrays<BusinessHours>

/** 修改当前企业通用设置。 */
export const updateOrganization = bind(UpdateOrganization)

/** 读取当前企业的客服工作时间。 */
export const getBusinessHours = bind(GetBusinessHours)

/** 修改当前企业的客服工作时间。 */
export const updateBusinessHours = bind(UpdateBusinessHours)

/** 读取当前企业的客服超时时长。 */
export const getServiceTimeouts = bind(GetServiceTimeouts)

/** 修改当前企业的客服超时时长。 */
export const updateServiceTimeouts = bind(UpdateServiceTimeouts)

/** 读取当前企业的会话小结设置。 */
export const getServiceSummarySettings = bind(GetServiceSummarySettings)

/** 修改当前企业的会话小结设置。 */
export const updateServiceSummarySettings = bind(UpdateServiceSummarySettings)

/** 读取当前企业的翻译设置。 */
export const getTranslationSettings = bind(GetTranslationSettings)

/** 修改当前企业的翻译设置。 */
export const updateTranslationSettings = bind(UpdateTranslationSettings)

/** 读取当前企业的客户身份密钥，未生成时为空。 */
export const getCustomerIdentitySecret = bind(GetCustomerIdentitySecret)

/** 生成或重新生成当前企业的客户身份密钥，旧密钥立即失效。 */
export const regenerateCustomerIdentitySecret = bind(RegenerateCustomerIdentitySecret)

/** 读取当前企业的咨询分类目录。 */
export const listServiceCategories = bind(ListServiceCategories)

/** 新增咨询分类。 */
export const createServiceCategory = bind(CreateServiceCategory)

/** 修改咨询分类。 */
export const updateServiceCategory = bind(UpdateServiceCategory)

/** 删除咨询分类。 */
export const deleteServiceCategory = bind(DeleteServiceCategory)

/** 修改当前用户的头像、姓名和邮箱。 */
export const updateProfile = bind(UpdateProfile)

/** 使用原生文件对话框选择图片。 */
export const selectImage = bind(SelectImage)

/** 修改当前用户的登录密码。 */
export const changePassword = bind(ChangePassword)

/** 修改当前用户偏好设置。 */
export const updateUserPreferences = bind(UpdateUserPreferences)

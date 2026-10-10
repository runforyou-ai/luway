/** 外部联系人调用。 */
import { ContactSort, type ContactListInput } from "@/api/generated/contract"
import * as ops from "@/api/generated/operations"

type ContactListQuery = Omit<Partial<ContactListInput>, "deleted">

/** 读取客户会话的客户身份与当前周期访客上下文。 */
export const getRequesterProfile = ops.getRequesterProfile

/** 读取客户会话当前客服周期内 AI 客服查询业务系统的记录。 */
export const listServiceBusinessQueries = ops.listServiceBusinessQueries

/** 将联系人移入回收站。 */
export const deleteContact = ops.deleteContact

/** 读取联系人详情。 */
export const getContact = ops.getContact

/** 创建联系人。 */
export const createContact = ops.createContact

/** 修改联系人。 */
export const updateContact = ops.updateContact

/** 恢复联系人。 */
export const restoreContact = ops.restoreContact

/** 读取当前企业的联系人字段。 */
export const listContactFields = ops.listContactFields

/** 新增联系人字段。 */
export const createContactField = ops.createContactField

/** 修改联系人字段。 */
export const updateContactField = ops.updateContactField

/** 删除联系人字段及其全部取值。 */
export const deleteContactField = ops.deleteContactField

/** 读取当前企业的联系人标签。 */
export const listContactTags = ops.listContactTags

/** 新增联系人标签。 */
export const createContactTag = ops.createContactTag

/** 修改联系人标签。 */
export const updateContactTag = ops.updateContactTag

/** 删除联系人标签并从所有联系人上移除。 */
export const deleteContactTag = ops.deleteContactTag

/** 填写或清空联系人字段。 */
export const setContactFieldValue = ops.setContactFieldValue

/** 给联系人添加标签。 */
export const addContactTag = ops.addContactTag

/** 移除联系人上的标签。 */
export const removeContactTag = ops.removeContactTag

/** 读取联系人列表。 */
export function listContacts(query: ContactListQuery, signal?: AbortSignal) {
  return listContactsByDeleted(query, false, signal)
}

/** 读取已删除的联系人列表。 */
export function listDeletedContacts(
  query: ContactListQuery,
  signal?: AbortSignal,
) {
  return listContactsByDeleted(query, true, signal)
}

/** 按是否回收站读取联系人列表。 */
function listContactsByDeleted(
  query: ContactListQuery,
  deleted: boolean,
  signal?: AbortSignal,
) {
  return ops.listContacts(
    {
      query: query.query ?? "",
      stage: query.stage,
      channelId: query.channelId ?? "",
      methodType: query.methodType,
      tagId: query.tagId ?? "",
      sort: query.sort ?? ContactSort.CreatedAtDescending,
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
      deleted,
    },
    signal,
  )
}

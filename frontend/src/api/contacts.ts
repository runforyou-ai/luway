/** 外部联系人调用。 */
import {
  AddContactTag,
  CreateContact,
  CreateContactField,
  CreateContactTag,
  DeleteContact,
  DeleteContactField,
  DeleteContactTag,
  GetContact,
  GetRequesterProfile,
  ListServiceBusinessQueries,
  ListContactFields,
  ListContactTags,
  ListContacts,
  RemoveContactTag,
  RestoreContact,
  SetContactFieldValue,
  UpdateContact,
  UpdateContactField,
  UpdateContactTag,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/service"
import {
  ContactFieldType,
  ContactSort,
  type Contact,
  type ContactField,
  type ContactFieldList,
  type ContactListInput,
} from "../../bindings/github.com/runforyou-ai/luway/internal/appservice/models"
import { bind } from "@/api/client"
import type { NonNullArrays } from "@/api/normalize"

export type ContactDetail = NonNullArrays<Contact>

type ContactFieldTypeId = Exclude<ContactFieldType, ContactFieldType.$zero>

export type ContactFieldData = Omit<NonNullArrays<ContactField>, "type"> & {
  type: ContactFieldTypeId
}

type ContactFieldListData = Omit<NonNullArrays<ContactFieldList>, "fields"> & { fields: ContactFieldData[] }

type ContactListQuery = Omit<Partial<ContactListInput>, "deleted">

const listContactsBound = bind(ListContacts)

/** 读取客户会话的客户身份与当前周期访客上下文。 */
export const getRequesterProfile = bind(GetRequesterProfile)

/** 读取客户会话当前客服周期内 AI 客服查询业务系统的记录。 */
export const listServiceBusinessQueries = bind(ListServiceBusinessQueries)

/** 将联系人移入回收站。 */
export const deleteContact = bind(DeleteContact)

/** 读取联系人详情。 */
export const getContact = bind(GetContact)

/** 创建联系人。 */
export const createContact = bind(CreateContact)

/** 修改联系人。 */
export const updateContact = bind(UpdateContact)

/** 恢复联系人。 */
export const restoreContact = bind(RestoreContact)

const listContactFieldsBound = bind(ListContactFields)

/** 读取当前企业的联系人字段。 */
export function listContactFields() {
  return listContactFieldsBound() as Promise<ContactFieldListData>
}

/** 新增联系人字段。 */
export const createContactField = bind(CreateContactField)

/** 修改联系人字段。 */
export const updateContactField = bind(UpdateContactField)

/** 删除联系人字段及其全部取值。 */
export const deleteContactField = bind(DeleteContactField)

/** 读取当前企业的联系人标签。 */
export const listContactTags = bind(ListContactTags)

/** 新增联系人标签。 */
export const createContactTag = bind(CreateContactTag)

/** 修改联系人标签。 */
export const updateContactTag = bind(UpdateContactTag)

/** 删除联系人标签并从所有联系人上移除。 */
export const deleteContactTag = bind(DeleteContactTag)

/** 填写或清空联系人字段。 */
export const setContactFieldValue = bind(SetContactFieldValue)

/** 给联系人添加标签。 */
export const addContactTag = bind(AddContactTag)

/** 移除联系人上的标签。 */
export const removeContactTag = bind(RemoveContactTag)

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
  return listContactsBound(
    {
      query: query.query ?? "",
      stage: query.stage ?? null,
      channelId: query.channelId ?? "",
      methodType: query.methodType ?? null,
      tagId: query.tagId ?? "",
      sort: query.sort ?? ContactSort.ContactSortCreatedAtDescending,
      page: query.page ?? 1,
      pageSize: query.pageSize ?? 50,
      deleted,
    },
    signal,
  )
}

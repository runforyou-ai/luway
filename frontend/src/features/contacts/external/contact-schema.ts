/** 联系人校验规则、详情与表单值转换及更新请求构造。 */
import { useMemo } from "react"
import { useTranslation } from "react-i18next"
import { z } from "zod"
import { isPossiblePhoneNumber } from "react-phone-number-input"

import {
  ContactMethodType,
  ContactStage,
  type Contact,
  type ContactInput,
  type ContactMethodInput,
} from "@/api"
import { enumSchema } from "@/lib/enum"

/** 联系人字段校验提示。 */
type ContactSchemaMessages = {
  channelRequired: string
  phoneInvalid: string
}

/** 联系人各字段的校验。 */
function createContactSchema(messages: ContactSchemaMessages) {
  return z.object({
    displayName: z.string().trim().max(200),
    channelId: z.string().uuid(messages.channelRequired),
    stage: enumSchema(ContactStage),
    email: z.union([z.literal(""), z.string().trim().email()]),
    phone: z
      .string()
      .refine(
        (value) => value === "" || isPossiblePhoneNumber(value),
        messages.phoneInvalid,
      ),
    notes: z.string().trim().max(5000),
  })
}

/** 手动新建联系人的校验：姓名、邮箱和电话至少填写一项。 */
function createNewContactSchema(
  messages: ContactSchemaMessages & { identityRequired: string },
) {
  return createContactSchema(messages).superRefine((value, context) => {
    if (!value.displayName && !value.email && !value.phone) {
      context.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["displayName"],
        message: messages.identityRequired,
      })
    }
  })
}

export type ContactFormValues = z.infer<ReturnType<typeof createContactSchema>>

/** 按当前语言读取联系人校验提示。 */
function useContactSchemaMessages() {
  const { t } = useTranslation("contacts")
  return useMemo(
    () => ({
      identityRequired: t("validation.identityRequired"),
      channelRequired: t("validation.channelRequired"),
      phoneInvalid: t("validation.phoneInvalid"),
    }),
    [t],
  )
}

/** 按当前语言创建编辑联系人的校验。 */
export function useContactSchema() {
  const messages = useContactSchemaMessages()
  return useMemo(() => createContactSchema(messages), [messages])
}

/** 按当前语言创建手动新建联系人的校验。 */
export function useNewContactSchema() {
  const messages = useContactSchemaMessages()
  return useMemo(() => createNewContactSchema(messages), [messages])
}

/** 把联系人详情转换为表单值，邮箱和电话取各自首项。 */
export function contactValuesFromDetail(detail: Contact): ContactFormValues {
  return {
    displayName: detail.contact.displayName ?? "",
    channelId: detail.contact.sourceChannelId,
    stage: detail.contact.stage,
    email:
      detail.methods.find(
        (method) => method.type === ContactMethodType.Email,
      )?.value ?? "",
    phone:
      detail.methods.find(
        (method) => method.type === ContactMethodType.Phone,
      )?.value ?? "",
    notes: detail.contact.notes ?? "",
  }
}

/** 用表单值更新每类联系方式的首项，其余项保持不变。 */
function contactMethodsFromDetail(
  detail: Contact,
  values: ContactFormValues,
): ContactMethodInput[] {
  const editedValues: Record<ContactMethodType, string> = {
    [ContactMethodType.Email]: values.email,
    [ContactMethodType.Phone]: values.phone,
  }
  const handled: Record<ContactMethodType, boolean> = {
    [ContactMethodType.Email]: false,
    [ContactMethodType.Phone]: false,
  }
  const methods: ContactMethodInput[] = []

  for (const method of detail.methods) {
    if (!handled[method.type]) {
      handled[method.type] = true
      const value = editedValues[method.type]
      if (!value) {
        continue
      }
      methods.push({
        type: method.type,
        value,
        label: method.label ?? "",
        isPrimary: method.isPrimary,
      })
      continue
    }
    methods.push({
      type: method.type,
      value: method.value,
      label: method.label ?? "",
      isPrimary: method.isPrimary,
    })
  }

  for (const type of [
    ContactMethodType.Email,
    ContactMethodType.Phone,
  ]) {
    if (!handled[type] && editedValues[type]) {
      methods.push({
        type,
        value: editedValues[type],
        label: "",
        isPrimary: true,
      })
    }
  }
  return methods
}

/** 基于现有联系人和编辑后的表单值生成更新请求，来源渠道保持不变。 */
export function contactUpdateInput(
  detail: Contact,
  values: ContactFormValues,
): ContactInput {
  return {
    displayName: values.displayName,
    channelId: detail.contact.sourceChannelId,
    stage: values.stage,
    notes: values.notes,
    methods: contactMethodsFromDetail(detail, values),
  }
}

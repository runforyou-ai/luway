/** 新建和编辑联系人表单，没有管理权限时只读展示。 */
import { useEffect, useRef } from "react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  ContactMethodType,
  ContactStage,
  createContact,
  isNotFoundApiError,
  updateContact,
  type ChannelOption,
  type Contact,
  type ContactInput,
} from "@/api"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { PhoneInput } from "@/components/ui/phone-input"
import { Textarea } from "@/components/ui/textarea"
import { channelTypeLabel, contactStageOptions } from "@/features/contacts/external/contact-labels"
import {
  contactUpdateInput,
  contactValuesFromDetail,
  useContactSchema,
  useNewContactSchema,
  type ContactFormValues,
} from "@/features/contacts/external/contact-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"

/** 创建联系人，或边改边存已有联系人；来源渠道创建后不可修改，readOnly 时禁用全部字段。 */
export function ContactForm({
  detail,
  channels,
  readOnly = false,
  onSaved,
  onCancel,
  onNotFound,
}: {
  detail?: Contact
  channels: ChannelOption[]
  readOnly?: boolean
  onSaved?: (detail: Contact) => void
  onCancel?: () => void
  onNotFound?: () => void
}) {
  const { t } = useTranslation(["contacts", "common"])
  const invalidate = useResourceInvalidator()
  const editSchema = useContactSchema()
  const newSchema = useNewContactSchema()
  const schema = detail ? editSchema : newSchema
  const form = useForm<ContactFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    // 编辑时离开字段即校验以便自动保存，新建时只在提交时校验。
    mode: detail ? "onBlur" : "onSubmit",
    defaultValues: detail
      ? contactValuesFromDetail(detail)
      : {
          displayName: "",
          channelId: "",
          stage: ContactStage.Visitor,
          email: "",
          phone: "",
          notes: "",
        },
  })
  const { submit, markSaved, mounted } = useFormSave({
    form,
    schema,
    autoSave: Boolean(detail) && !readOnly,
    save: async (values) => {
      if (detail) {
        try {
          const saved = await updateContact(detail.contact.id, contactUpdateInput(detail, values))
          void invalidate(resourceKeys.contact(saved.contact.id))
          void invalidate(resourceKeys.contacts())
          return saved
        } catch (error) {
          // 联系人已不存在时只在表单仍打开时关闭详情，错误仍由统一流程提示。
          if (isNotFoundApiError(error) && mounted.current) onNotFound?.()
          throw error
        }
      }
      const input: ContactInput = {
        displayName: values.displayName,
        channelId: values.channelId,
        stage: values.stage,
        notes: values.notes,
        methods: [
          ...(values.email
            ? [{ type: ContactMethodType.Email, value: values.email, label: "", isPrimary: true }]
            : []),
          ...(values.phone
            ? [{ type: ContactMethodType.Phone, value: values.phone, label: "", isPrimary: true }]
            : []),
        ],
      }
      const saved = await createContact(input)
      void invalidate(resourceKeys.contacts())
      return saved
    },
    onSubmitted: (saved) => {
      toast.success(t("form.created"))
      onSaved?.(saved)
    },
    savedValues: (saved) => contactValuesFromDetail(saved),
    errorFields: ["displayName", "channelId", "stage", "methods", "notes"],
    logLabel: detail ? "保存联系人" : "创建联系人",
  })

  // 渲染时读取 isDirty 以订阅草稿状态，供详情刷新时判断。
  const dirty = useRef(false)
  dirty.current = form.formState.isDirty

  // 联系人详情刷新时同步未修改的表单和自动保存基准，保留正在编辑的草稿。
  useEffect(() => {
    if (!detail || dirty.current) return
    const values = contactValuesFromDetail(detail)
    form.reset(values)
    markSaved(values)
  }, [detail, form])

  return (
    <form
      className="space-y-9"
      onSubmit={form.handleSubmit(submit)}
      noValidate
    >
      {/* 没有管理权限时禁用全部字段。 */}
      <fieldset disabled={readOnly} className="min-w-0">
        <FieldGroup className="gap-5">
          <FormInputField
            name="displayName"
            control={form.control}
            label={t("form.displayName")}
            required={false}
            autoFocus={!detail}
          />

          <Controller
            name="channelId"
            control={form.control}
            render={({ field, fieldState }) => (
              <Field data-invalid={fieldState.invalid}>
                <FieldLabel htmlFor={field.name} required={!detail}>
                  {t("form.channel")}
                </FieldLabel>
                <NativeSelect
                  {...field}
                  id={field.name}
                  required
                  disabled={Boolean(detail)}
                  aria-invalid={fieldState.invalid}
                >
                  {detail ? (
                    <option value={detail.contact.sourceChannelId}>
                      {channelTypeLabel(detail.sourceChannel.type, t)} · {detail.sourceChannel.name}
                    </option>
                  ) : (
                    <>
                      <option value="" disabled>
                        {t("form.channelPlaceholder")}
                      </option>
                      {channels.map((channel) => (
                        <option key={channel.id} value={channel.id}>
                          {channelTypeLabel(channel.type, t)} · {channel.name}
                        </option>
                      ))}
                    </>
                  )}
                </NativeSelect>
              </Field>
            )}
          />

          <Controller
            name="stage"
            control={form.control}
            render={({ field, fieldState }) => (
              <Field data-invalid={fieldState.invalid}>
                <FieldLabel htmlFor={field.name} required>
                  {t("form.stage")}
                </FieldLabel>
                <NativeSelect
                  {...field}
                  id={field.name}
                  required
                  aria-invalid={fieldState.invalid}
                >
                  {contactStageOptions.map((item) => (
                    <option key={item.value} value={item.value}>{t(item.label)}</option>
                  ))}
                </NativeSelect>
              </Field>
            )}
          />

          <FormInputField
            name="email"
            control={form.control}
            label={t("form.email")}
            type="email"
            required={false}
          />

          <Controller
            name="phone"
            control={form.control}
            render={({ field, fieldState }) => (
              <Field>
                <FieldLabel htmlFor={field.name}>{t("form.phone")}</FieldLabel>
                <PhoneInput
                  ref={field.ref}
                  id={field.name}
                  name={field.name}
                  value={field.value}
                  onChange={field.onChange}
                  onBlur={field.onBlur}
                  aria-invalid={fieldState.invalid}
                  autoComplete="tel"
                />
              </Field>
            )}
          />

          <Controller
            name="notes"
            control={form.control}
            render={({ field, fieldState }) => (
              <Field data-invalid={fieldState.invalid}>
                <FieldLabel htmlFor={field.name}>{t("form.notes")}</FieldLabel>
                <Textarea
                  {...field}
                  id={field.name}
                  aria-invalid={fieldState.invalid}
                />
              </Field>
            )}
          />

          {detail && detail.channelIdentities.length > 0 ? (
            <Field>
              <FieldLabel>{t("detail.linkedChannels")}</FieldLabel>
              <div className="grid gap-2 text-sm">
                {detail.channelIdentities.map((identity) => (
                  <div key={`${identity.channelId}:${identity.externalId}`}>
                    <div>{identity.channelName}</div>
                    <div className="text-xs text-muted-foreground">
                      {identity.displayName || identity.externalId}
                    </div>
                  </div>
                ))}
              </div>
            </Field>
          ) : null}
        </FieldGroup>
      </fieldset>
      <FormActions
        saving={form.formState.isSubmitting}
        onCancel={onCancel}
        submit={!detail}
      />
    </form>
  )
}

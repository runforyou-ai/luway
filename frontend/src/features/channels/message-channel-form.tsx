/** 消息渠道基础信息表单。 */
import { useMemo } from "react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  ChannelType,
  ChannelRoutingTargetType,
  CustomerLocale,
  createMessageChannel,
  isNotFoundApiError,
  updateMessageChannel,
  type MessageChannelSummary,
} from "@/api"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import { Textarea } from "@/components/ui/textarea"
import { ChannelReceptionSettingsFields } from "@/features/channels/reception/channel-reception-settings-fields"
import {
  createMessageChannelSchema,
  type MessageChannelFormValues,
} from "@/features/channels/message-channel-schema"
import { useAutoSave } from "@/hooks/use-auto-save"
import { useFormLifetime } from "@/hooks/use-form-lifetime"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { useReturnTo } from "@/hooks/use-return-to"
import { requestErrorMessage } from "@/lib/form-errors"
import { languageDisplayName } from "@/lib/languages"
import { recoverSession } from "@/lib/session-navigation"
import { cn } from "@/lib/utils"
import { zodResolver } from "@/lib/zod-resolver"

/** 创建或修改消息渠道基础信息。 */
export function MessageChannelForm({
  channel,
  type = ChannelType.ChannelTypeWebsite,
  onUpdated,
}: {
  channel?: MessageChannelSummary
  /** 新建时的渠道类别，由所在的渠道二级菜单决定，表单内不再选择。 */
  type?: ChannelType
  onUpdated?: (value: MessageChannelSummary) => void
}) {
  const { t, i18n } = useTranslation(["channels", "common"])
  const navigate = useNavigate()
  const { returnTo, leave } = useReturnTo("/channels")
  const invalidateResource = useResourceInvalidator()
  const schema = useMemo(
    () =>
      createMessageChannelSchema({
        nameRequired: t("validation.nameRequired"),
        nameTooLong: t("validation.nameTooLong"),
        descriptionTooLong: t("validation.descriptionTooLong"),
        teamRequired: t("validation.teamRequired"),
        memberRequired: t("validation.memberRequired"),
        fallbackDifferent: t("validation.fallbackDifferent"),
      }),
    [t],
  )
  const form = useForm<MessageChannelFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    // 编辑时离开字段即校验以便自动保存；新建时等提交再校验，避免原生校验把焦点锁在必填项上。
    mode: channel ? "onBlur" : "onSubmit",
    defaultValues: {
      type: channel?.type ?? type,
      name: channel?.name ?? "",
      description: channel?.description ?? "",
      defaultLocale: channel?.defaultLocale ?? CustomerLocale.CustomerLocaleChineseSimplified,
      newConversationTarget: channel?.newConversationTarget ?? {
        type: ChannelRoutingTargetType.ChannelRoutingTargetTypePublicQueue,
        id: "",
      },
      fallbackTarget: channel?.fallbackTarget ?? {
        type: ChannelRoutingTargetType.ChannelRoutingTargetTypePublicQueue,
        id: "",
      },
    },
  })
  // 新建时登记未保存内容，离开前确认。
  const { dirty, mounted } = useFormLifetime(!channel && form.formState.isDirty)
  // 已有渠道时边改边存，新建仍由底部按钮提交。
  const { acceptSaved, saveNow } = useAutoSave({
    form,
    schema,
    save: submit,
    enabled: Boolean(channel),
  })

  /** 提交消息渠道基础信息。 */
  async function submit(values: MessageChannelFormValues) {
    try {
      if (channel) {
        const updated = await updateMessageChannel(channel.id, {
          name: values.name,
          description: values.description,
          defaultLocale: values.defaultLocale,
        })
        const next = {
          type: updated.type,
          name: updated.name,
          description: updated.description ?? "",
          defaultLocale: updated.defaultLocale,
          newConversationTarget: values.newConversationTarget,
          fallbackTarget: values.fallbackTarget,
        }
        acceptSaved(values, next)
        onUpdated?.(updated)
        void invalidateResource(resourceKeys.messageChannels())
        void invalidateResource(resourceKeys.channelOptions())
        return true
      }

      const created = await createMessageChannel(values)
      void invalidateResource(resourceKeys.messageChannels())
      void invalidateResource(resourceKeys.channelOptions())
      if (!mounted.current) return true
      dirty.current = false
      form.reset(values)
      toast.success(t("create.success"))
      navigate(
        `/channels/${created.type}/${created.id}?${new URLSearchParams({ tab: "basic", returnTo })}`,
        { replace: true },
      )
      return true
    } catch (error) {
      if (!channel && !mounted.current) return false
      if (recoverSession(error, navigate)) {
        return false
      }
      if (channel && isNotFoundApiError(error)) {
        console.warn("消息渠道不存在", { channel_id: channel.id })
        leave({ replace: true })
        return false
      }
      console.warn("保存消息渠道失败", error)
      toast.error(
        requestErrorMessage(error, [
          "type",
          "name",
          "description",
          "defaultLocale",
          "newConversationTarget",
          "fallbackTarget",
        ]),
      )
      return false
    }
  }

  const { isSubmitting } = form.formState
  const channelType = form.watch("type")

  return (
    <form
      className={cn("w-full", channel ? undefined : "space-y-9")}
      onSubmit={form.handleSubmit((values) => channel ? saveNow() : submit(values))}
      noValidate
    >
      <FieldGroup>

        <FormInputField
          name="name"
          control={form.control}
          label={t("form.name")}
          autoFocus
        />

        <Controller
          name="description"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name}>
                {t("form.description")}
              </FieldLabel>
              <Textarea
                {...field}
                id={field.name}
                aria-invalid={fieldState.invalid}
              />
            </Field>
          )}
        />

        <Controller
          name="defaultLocale"
          control={form.control}
          render={({ field, fieldState }) => (
            <Field data-invalid={fieldState.invalid}>
              <FieldLabel htmlFor={field.name} required>
                {t("form.defaultLocale")}
              </FieldLabel>
              <NativeSelect
                {...field}
                id={field.name}
                required
                aria-invalid={fieldState.invalid}
              >
                {/* 对客语言按主语言显示名称。 */}
                {Object.values(CustomerLocale)
                  .filter((locale) => locale !== CustomerLocale.$zero)
                  .map((locale) => (
                    <option key={locale} value={locale}>
                      {languageDisplayName(locale.split("-")[0], i18n.language)}
                    </option>
                  ))}
              </NativeSelect>
            </Field>
          )}
        />

        {!channel ? (
          <ChannelReceptionSettingsFields
            control={form.control}
            channelType={channelType}
          />
        ) : null}

      </FieldGroup>
      {channel ? null : (
        <FormActions saving={isSubmitting} cancelTo={returnTo} />
      )}
    </form>
  )
}

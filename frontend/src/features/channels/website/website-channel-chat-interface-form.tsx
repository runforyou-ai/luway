/** 网站渠道聊天窗口外观与对话功能表单，两组字段分属聊天窗口的外观与对话子页签。 */
import { useEffect, useId, useMemo } from "react"
import { Controller, useForm, useWatch, type Control } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  isNotFoundApiError,
  updateWebsiteChannelChatInterface,
  type WebsiteChannel,
  type WebsiteChannelChatInterface,
  type WebsiteChannelChatInterfaceInput,
} from "@/api"
import { FormInputField } from "@/components/form/form-input-field"
import { SwitchField } from "@/components/form/switch-field"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { TabsContent } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import {
  createWebsiteChannelChatInterfaceSchema,
  defaultWebsiteChannelThemeColor,
  isWebsiteChannelThemeColor,
  type WebsiteChannelChatInterfaceFormValues,
} from "@/features/channels/website/website-channel-chat-interface-schema"
import { useAutoSave } from "@/hooks/use-auto-save"
import { useReturnTo } from "@/hooks/use-return-to"
import { requestErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 主题色的预设选项。 */
const presetColors = [
  defaultWebsiteChannelThemeColor,
  "#16A34A",
  "#9333EA",
  "#E11D48",
  "#EA580C",
]

/** 修改网站渠道聊天窗口外观与对话功能，在所属页签中渲染外观与对话子页签内容。 */
export function WebsiteChannelChatInterfaceForm({
  channel,
  onPreviewChange,
  onUpdated,
}: {
  channel: WebsiteChannel
  onPreviewChange: (value: WebsiteChannelChatInterfaceInput) => void
  onUpdated: (value: WebsiteChannelChatInterface) => void
}) {
  const { t } = useTranslation(["channels", "common"])
  const navigate = useNavigate()
  const { leave } = useReturnTo("/channels")
  const formId = useId()
  const schema = useMemo(
    () =>
      createWebsiteChannelChatInterfaceSchema({
        titleTooLong: t("chatInterface.validation.titleTooLong"),
        greetingTooLong: t("chatInterface.validation.greetingTooLong"),
        themeColorInvalid: t("chatInterface.validation.themeColorInvalid"),
      }),
    [t]
  )
  const form = useForm<WebsiteChannelChatInterfaceFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    mode: "onBlur",
    defaultValues: {
      title: channel.chatInterface.title,
      greetingMessage: channel.chatInterface.greetingMessage ?? "",
      themeColor: channel.chatInterface.themeColor,
      attachmentsEnabled: channel.chatInterface.attachmentsEnabled,
      emojiEnabled: channel.chatInterface.emojiEnabled,
      ratingEnabled: channel.chatInterface.ratingEnabled,
      multipleConversationsEnabled:
        channel.chatInterface.multipleConversationsEnabled,
    },
  })
  // 草稿字段缺失时沿用已保存设置。
  const saved = channel.chatInterface
  const previewValue = useWatch({
    control: form.control,
    compute: (value): WebsiteChannelChatInterfaceInput => ({
      title: value.title ?? saved.title,
      greetingMessage: value.greetingMessage ?? saved.greetingMessage ?? "",
      themeColor: value.themeColor ?? saved.themeColor,
      attachmentsEnabled: value.attachmentsEnabled ?? saved.attachmentsEnabled,
      emojiEnabled: value.emojiEnabled ?? saved.emojiEnabled,
      ratingEnabled: value.ratingEnabled ?? saved.ratingEnabled,
      multipleConversationsEnabled:
        value.multipleConversationsEnabled ??
        saved.multipleConversationsEnabled,
    }),
  })

  useEffect(() => {
    onPreviewChange(previewValue)
  }, [onPreviewChange, previewValue])

  const { acceptSaved, saveNow } = useAutoSave({ form, schema, save: submit })

  /** 提交聊天窗口设置。 */
  async function submit(values: WebsiteChannelChatInterfaceFormValues) {
    try {
      const updated = await updateWebsiteChannelChatInterface(channel.id, values)
      const next = {
        title: updated.title,
        greetingMessage: updated.greetingMessage ?? "",
        themeColor: updated.themeColor,
        attachmentsEnabled: updated.attachmentsEnabled,
        emojiEnabled: updated.emojiEnabled,
        ratingEnabled: updated.ratingEnabled,
        multipleConversationsEnabled: updated.multipleConversationsEnabled,
      }
      acceptSaved(values, next)
      onUpdated(updated)
      return true
    } catch (error) {
      if (recoverSession(error, navigate)) {
        return false
      }
      if (isNotFoundApiError(error)) {
        console.warn("网站渠道不存在", { channel_id: channel.id })
        leave({ replace: true })
        return false
      }
      console.warn("保存网站渠道聊天窗口设置失败", error)
      toast.error(
        requestErrorMessage(error, [
          "title",
          "greetingMessage",
          "themeColor",
          "attachmentsEnabled",
          "emojiEnabled",
          "ratingEnabled",
          "multipleConversationsEnabled",
        ]),
      )
      return false
    }
  }

  return (
    <form
      className="w-full"
      onSubmit={form.handleSubmit(() => saveNow())}
      noValidate
    >
      <TabsContent
        value="appearance"
        forceMount
        className="mt-6 data-[state=inactive]:hidden"
      >
        <FieldGroup>
          <FormInputField
            name="title"
            id={`${formId}-title`}
            control={form.control}
            label={t("chatInterface.form.title")}
            autoFocus
          />

          <Controller
            name="greetingMessage"
            control={form.control}
            render={({ field, fieldState }) => (
              <Field data-invalid={fieldState.invalid}>
                <FieldLabel htmlFor={`${formId}-${field.name}`}>
                  {t("chatInterface.form.greetingMessage")}
                </FieldLabel>
                <Textarea
                  {...field}
                  id={`${formId}-${field.name}`}
                  rows={4}
                  aria-invalid={fieldState.invalid}
                />
              </Field>
            )}
          />

          <Controller
            name="themeColor"
            control={form.control}
            render={({ field, fieldState }) => {
              const colorValue = isWebsiteChannelThemeColor(field.value)
                ? field.value
                : defaultWebsiteChannelThemeColor
              return (
                <Field data-invalid={fieldState.invalid}>
                  <FieldLabel htmlFor={`${formId}-${field.name}`} required>
                    {t("chatInterface.form.themeColor")}
                  </FieldLabel>
                  <div className="flex flex-wrap items-center gap-2.5">
                    {presetColors.map((color) => (
                      <button
                        key={color}
                        type="button"
                        className="size-8 rounded-full ring-offset-2 aria-pressed:ring-2 aria-pressed:ring-foreground focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none"
                        style={{ backgroundColor: color }}
                        aria-label={color}
                        aria-pressed={field.value.toUpperCase() === color}
                        title={color}
                        onClick={() => field.onChange(color)}
                      />
                    ))}
                    <input
                      type="color"
                      className="h-9 w-12 rounded-md border bg-transparent p-1"
                      value={colorValue}
                      aria-label={t("chatInterface.form.colorPicker")}
                      onChange={(event) =>
                        field.onChange(event.target.value.toUpperCase())
                      }
                    />
                    <Input
                      {...field}
                      id={`${formId}-${field.name}`}
                      className="w-32 font-mono uppercase"
                      maxLength={7}
                      required
                      aria-invalid={fieldState.invalid}
                    />
                  </div>
                </Field>
              )
            }}
          />
        </FieldGroup>
      </TabsContent>

      <TabsContent
        value="conversation"
        forceMount
        className="mt-6 data-[state=inactive]:hidden"
      >
        <FieldGroup>
          <ChatInterfaceSwitch
            control={form.control}
            idPrefix={formId}
            name="attachmentsEnabled"
            label={t("chatInterface.form.attachmentsEnabled")}
          />
          <ChatInterfaceSwitch
            control={form.control}
            idPrefix={formId}
            name="emojiEnabled"
            label={t("chatInterface.form.emojiEnabled")}
          />
          <ChatInterfaceSwitch
            control={form.control}
            idPrefix={formId}
            name="ratingEnabled"
            label={t("chatInterface.form.ratingEnabled")}
            description={t("chatInterface.form.ratingEnabledDescription")}
          />
          <ChatInterfaceSwitch
            control={form.control}
            idPrefix={formId}
            name="multipleConversationsEnabled"
            label={t("chatInterface.form.multipleConversationsEnabled")}
            description={t(
              "chatInterface.form.multipleConversationsEnabledDescription"
            )}
          />
        </FieldGroup>
      </TabsContent>
    </form>
  )
}

/** 绑定到聊天窗口表单布尔字段的开关。 */
function ChatInterfaceSwitch({
  control,
  idPrefix,
  name,
  label,
  description,
}: {
  control: Control<WebsiteChannelChatInterfaceFormValues>
  idPrefix: string
  name:
    | "attachmentsEnabled"
    | "emojiEnabled"
    | "ratingEnabled"
    | "multipleConversationsEnabled"
  label: string
  description?: string
}) {
  return (
    <Controller
      name={name}
      control={control}
      render={({ field }) => (
        <SwitchField
          id={`${idPrefix}-${field.name}`}
          name={field.name}
          label={label}
          description={description}
          checked={field.value}
          onBlur={field.onBlur}
          onCheckedChange={field.onChange}
          ref={field.ref}
        />
      )}
    />
  )
}

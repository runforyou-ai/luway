/** 网站渠道聊天窗口首页表单。 */
import { useEffect, useId, useMemo } from "react"
import {
  Controller,
  useFieldArray,
  useForm,
  useWatch,
  type Control,
} from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  isNotFoundApiError,
  updateWebsiteChannelHome,
  WebsiteHomeBlockType,
  type WebsiteChannel,
  type WebsiteChannelHomeInput,
} from "@/api"
import { ArrayRowActions } from "@/components/array-row-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { SwitchField } from "@/components/form/switch-field"
import { Button } from "@/components/ui/button"
import { FieldDescription, FieldGroup } from "@/components/ui/field"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import {
  createWebsiteChannelHomeSchema,
  isBlankWebsiteHomeLink,
  type WebsiteChannelHomeFormValues,
} from "@/features/channels/website/website-channel-home-schema"
import { useAutoSave } from "@/hooks/use-auto-save"
import { useReturnTo } from "@/hooks/use-return-to"
import { requestErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 首页卡片类型对应的名称文案键。 */
const blockLabelKeys = {
  [WebsiteHomeBlockType.WebsiteHomeBlockRecentConversation]:
    "home.form.blocks.recentConversation",
  [WebsiteHomeBlockType.WebsiteHomeBlockStartConversation]:
    "home.form.blocks.startConversation",
  [WebsiteHomeBlockType.WebsiteHomeBlockLinks]: "home.form.blocks.links",
} as const satisfies Record<WebsiteHomeBlockType, string>

/** 把服务端首页设置转换为表单值。 */
function homeFormValues(
  home: WebsiteChannel["home"]
): WebsiteChannelHomeFormValues {
  return {
    enabled: home.enabled,
    welcome: home.welcome,
    headline: home.headline,
    blocks: home.blocks.map((block) => ({
      type: block.type as WebsiteHomeBlockType,
      enabled: block.enabled,
    })),
    links: home.links,
  }
}

/** 修改网站渠道聊天窗口首页开关、问候语、卡片与链接。 */
export function WebsiteChannelHomeForm({
  channel,
  onPreviewChange,
  onUpdated,
}: {
  channel: WebsiteChannel
  onPreviewChange: (value: WebsiteChannelHomeInput) => void
  onUpdated: () => void
}) {
  const { t } = useTranslation(["channels", "common"])
  const navigate = useNavigate()
  const { leave } = useReturnTo("/channels")
  const formId = useId()
  const schema = useMemo(
    () =>
      createWebsiteChannelHomeSchema({
        welcomeTooLong: t("home.validation.welcomeTooLong"),
        headlineTooLong: t("home.validation.headlineTooLong"),
        linkTitleRequired: t("home.validation.linkTitleRequired"),
        linkTitleTooLong: t("home.validation.linkTitleTooLong"),
        linkURLInvalid: t("home.validation.linkURLInvalid"),
      }),
    [t]
  )
  const form = useForm<WebsiteChannelHomeFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    mode: "onBlur",
    defaultValues: homeFormValues(channel.home),
  })
  const previewValue = useWatch({
    control: form.control,
    compute: (value): WebsiteChannelHomeInput => ({
      enabled: value.enabled ?? channel.home.enabled,
      welcome: value.welcome ?? channel.home.welcome,
      headline: value.headline ?? channel.home.headline,
      blocks: (value.blocks ?? []).flatMap((block) =>
        block?.type
          ? [{ type: block.type, enabled: block.enabled ?? false }]
          : []
      ),
      links: (value.links ?? []).flatMap((link) => {
        const item = { title: link?.title ?? "", url: link?.url ?? "" }
        return isBlankWebsiteHomeLink(item) ? [] : [item]
      }),
    }),
  })

  useEffect(() => {
    onPreviewChange(previewValue)
  }, [onPreviewChange, previewValue])

  const { acceptSaved, markSaved, saveNow } = useAutoSave({
    form,
    schema,
    save: submit,
  })

  /** 提交首页设置。 */
  async function submit(values: WebsiteChannelHomeFormValues) {
    try {
      const updated = await updateWebsiteChannelHome(channel.id, values)
      // 表单中仍有空链接行时只更新保存基准，保留这些行继续编辑。
      if (form.getValues("links").some(isBlankWebsiteHomeLink)) {
        markSaved(homeFormValues(updated))
      } else {
        acceptSaved(values, homeFormValues(updated))
      }
      onUpdated()
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
      console.warn("保存网站渠道聊天窗口首页失败", error)
      toast.error(requestErrorMessage(error, ["enabled", "welcome", "headline", "blocks", "links"]))
      return false
    }
  }

  return (
    <form
      className="w-full"
      onSubmit={form.handleSubmit(() => saveNow())}
      noValidate
    >
      <FieldGroup>
        <Controller
          name="enabled"
          control={form.control}
          render={({ field }) => (
            <SwitchField
              id={`${formId}-${field.name}`}
              name={field.name}
              label={t("home.form.enabled")}
              description={t("home.form.enabledDescription")}
              checked={field.value}
              onBlur={field.onBlur}
              onCheckedChange={field.onChange}
              ref={field.ref}
            />
          )}
        />

        <div
          className="space-y-3"
          role="group"
          aria-labelledby={`${formId}-greeting-label`}
        >
          <div>
            <div id={`${formId}-greeting-label`} className="text-sm font-medium">
              {t("home.form.greeting")}
            </div>
            <FieldDescription className="mt-1">
              {t("home.form.greetingDescription")}
            </FieldDescription>
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <FormInputField
              control={form.control}
              name="welcome"
              id={`${formId}-welcome`}
              label={t("home.form.welcome")}
              required={false}
            />
            <FormInputField
              control={form.control}
              name="headline"
              id={`${formId}-headline`}
              label={t("home.form.headline")}
              required={false}
            />
          </div>
        </div>

        <HomeBlockFields control={form.control} />

        <HomeLinkFields control={form.control} />
      </FieldGroup>
    </form>
  )
}

/** 编辑首页卡片的开关与展示顺序。 */
function HomeBlockFields({
  control,
}: {
  control: Control<WebsiteChannelHomeFormValues>
}) {
  const { t } = useTranslation("channels")
  const id = useId()
  const { fields, move } = useFieldArray({
    control,
    name: "blocks",
    keyName: "fieldKey",
  })
  return (
    <div className="space-y-3" role="group" aria-labelledby={`${id}-label`}>
      <div>
        <div id={`${id}-label`} className="text-sm font-medium">
          {t("home.form.blocks.title")}
        </div>
        <FieldDescription className="mt-1">
          {t("home.form.blocks.description")}
        </FieldDescription>
      </div>
      <div className="divide-y rounded-lg border">
        {fields.map((item, index) => {
          const name = t(blockLabelKeys[item.type])
          const switchId = `${id}-${item.fieldKey}`
          return (
            <div className="flex items-center gap-3 px-4 py-2" key={item.fieldKey}>
              <Controller
                name={`blocks.${index}.enabled`}
                control={control}
                render={({ field }) => (
                  <Switch
                    id={switchId}
                    name={field.name}
                    checked={field.value}
                    onBlur={field.onBlur}
                    onCheckedChange={field.onChange}
                    ref={field.ref}
                  />
                )}
              />
              <Label htmlFor={switchId} className="min-w-0 flex-1">
                {name}
              </Label>
              <ArrayRowActions
                className="shrink-0"
                index={index}
                count={fields.length}
                onMove={move}
                moveUpLabel={t("home.form.blocks.moveUp", { name })}
                moveDownLabel={t("home.form.blocks.moveDown", { name })}
              />
            </div>
          )
        })}
      </div>
    </div>
  )
}

/** 编辑聊天窗口首页按顺序展示的链接列表。 */
function HomeLinkFields({
  control,
}: {
  control: Control<WebsiteChannelHomeFormValues>
}) {
  const { t } = useTranslation("channels")
  const id = useId()
  const { fields, append, remove, move } = useFieldArray({
    control,
    name: "links",
    keyName: "fieldKey",
  })
  return (
    <div className="space-y-3" role="group" aria-labelledby={`${id}-label`}>
      <div>
        <div id={`${id}-label`} className="text-sm font-medium">
          {t("home.form.links.title")}
        </div>
        <FieldDescription className="mt-1">
          {t("home.form.links.description")}
        </FieldDescription>
      </div>
      {fields.length > 0 ? (
        <div className="divide-y rounded-lg border">
          {fields.map((item, index) => (
            <div
              className="flex items-end gap-3 px-4 py-3"
              key={item.fieldKey}
            >
              <div className="grid min-w-0 flex-1 gap-3 sm:grid-cols-2">
                <FormInputField
                  control={control}
                  name={`links.${index}.title`}
                  // 标题与地址的必填互相依赖，任一变化时一并重新校验。
                  deps={[`links.${index}.url`]}
                  id={`${id}-${item.fieldKey}-title`}
                  label={t("home.form.links.linkTitle")}
                />
                <FormInputField
                  control={control}
                  name={`links.${index}.url`}
                  deps={[`links.${index}.title`]}
                  id={`${id}-${item.fieldKey}-url`}
                  label={t("home.form.links.linkURL")}
                  type="url"
                />
              </div>
              <ArrayRowActions
                className="shrink-0"
                index={index}
                count={fields.length}
                onMove={move}
                moveUpLabel={t("home.form.links.moveUp", { number: index + 1 })}
                moveDownLabel={t("home.form.links.moveDown", { number: index + 1 })}
                remove={{
                  label: t("home.form.links.remove", { number: index + 1 }),
                  onRemove: () => remove(index),
                }}
              />
            </div>
          ))}
        </div>
      ) : null}
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => append({ title: "", url: "" })}
      >
        {t("home.form.links.add")}
      </Button>
    </div>
  )
}

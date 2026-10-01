/** 网站渠道帮助中心表单。 */
import { useEffect, useId } from "react"
import { Controller, useForm, useWatch } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"
import { z } from "zod"

import {
  isNotFoundApiError,
  listKnowledgeBases,
  updateWebsiteChannelHelpCenter,
  type WebsiteChannelData,
} from "@/api"
import { AgentResourcePickerField } from "@/components/agent-fields/agent-resource-picker-field"
import { SwitchField } from "@/components/form/switch-field"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import type { WebsiteHelpCenterPreviewDraft } from "@/features/channels/website/website-chat-preview"
import { resourceKeys } from "@/hooks/resource-keys"
import { useAutoSave } from "@/hooks/use-auto-save"
import { useReturnTo } from "@/hooks/use-return-to"
import { requestErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

const helpCenterSchema = z.object({
  enabled: z.boolean(),
  knowledgeBaseIds: z.array(z.string()),
})

type WebsiteChannelHelpCenterFormValues = z.infer<typeof helpCenterSchema>

/** 设置网站渠道帮助页签开关与发布的知识库。 */
export function WebsiteChannelHelpCenterForm({
  channel,
  onPreviewChange,
  onUpdated,
}: {
  channel: WebsiteChannelData
  onPreviewChange: (value: WebsiteHelpCenterPreviewDraft) => void
  onUpdated: () => void
}) {
  const { t } = useTranslation(["channels", "common"])
  const navigate = useNavigate()
  const { leave } = useReturnTo("/channels")
  const formId = useId()
  const form = useForm<WebsiteChannelHelpCenterFormValues>({
    resolver: zodResolver(helpCenterSchema),
    shouldUseNativeValidation: true,
    mode: "onBlur",
    defaultValues: {
      enabled: channel.helpCenter.enabled,
      knowledgeBaseIds: channel.helpCenter.knowledgeBaseIds,
    },
  })
  const previewValue = useWatch({
    control: form.control,
    compute: (value): WebsiteHelpCenterPreviewDraft => ({
      helpEnabled: value.enabled ?? channel.helpCenter.enabled,
    }),
  })

  useEffect(() => {
    onPreviewChange(previewValue)
  }, [onPreviewChange, previewValue])

  const { acceptSaved, saveNow } = useAutoSave({
    form,
    schema: helpCenterSchema,
    save: submit,
  })

  /** 提交帮助页签开关与发布的知识库。 */
  async function submit(values: WebsiteChannelHelpCenterFormValues) {
    try {
      const updated = await updateWebsiteChannelHelpCenter(channel.id, values)
      acceptSaved(values, {
        enabled: updated.enabled,
        knowledgeBaseIds: updated.knowledgeBaseIds,
      })
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
      console.warn("保存网站渠道帮助中心失败", error)
      toast.error(requestErrorMessage(error, ["enabled", "knowledgeBaseIds"]))
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
              label={t("helpCenter.enabled")}
              description={t("helpCenter.enabledDescription")}
              checked={field.value}
              onBlur={field.onBlur}
              onCheckedChange={field.onChange}
              ref={field.ref}
            />
          )}
        />
        <Controller
          name="knowledgeBaseIds"
          control={form.control}
          render={({ field }) => (
            <Field>
              <FieldLabel>{t("helpCenter.knowledgeBases")}</FieldLabel>
              <AgentResourcePickerField
                value={field.value}
                onChange={field.onChange}
                disabled={form.formState.isSubmitting}
                resourceKey={resourceKeys.knowledgeBases()}
                load={() => listKnowledgeBases()}
                toOptions={(data) =>
                  data.knowledgeBases.map((base) => ({
                    id: base.id,
                    name: base.name,
                    detail: base.description,
                  }))
                }
                labels={{
                  title: t("helpCenter.pickerTitle"),
                  group: t("helpCenter.knowledgeBases"),
                  unconfigured: t("helpCenter.unconfigured"),
                  selected: (count, names) =>
                    names === ""
                      ? t("helpCenter.selected", { count })
                      : count === 1
                        ? t("helpCenter.selectedOne", { names })
                        : t("helpCenter.selectedNames", { names, count }),
                  empty: t("helpCenter.empty"),
                  loadError: t("helpCenter.loadError"),
                }}
              />
              <FieldDescription>
                {t("helpCenter.knowledgeBasesHelp")}
              </FieldDescription>
            </Field>
          )}
        />
      </FieldGroup>
    </form>
  )
}

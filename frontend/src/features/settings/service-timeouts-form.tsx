/** 企业客服分配与提醒设置表单。 */
import { useMemo } from "react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { z } from "zod"

import { getServiceTimeouts, updateServiceTimeouts } from "@/api"
import { ResourceContent } from "@/components/resource-content"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"

/** 表单中的时长字段，按页面顺序排列。 */
const timeoutFields = [
  "responseReminderMinutes",
  "responseReclaimMinutes",
  "queueReminderMinutes",
  "aiFollowUpMinutes",
  "aiCloseMinutes",
] as const

type ServiceTimeoutsFormValues = Record<(typeof timeoutFields)[number], string>

/** 创建超时时长校验：各时长为正整数分钟，回收时长大于提醒时长。 */
function createServiceTimeoutsSchema(messages: {
  minutesInvalid: string
  reclaimAfterReminder: string
}) {
  const minutes = z.string().trim().regex(/^[1-9]\d*$/, messages.minutesInvalid)
  return z
    .object({
      responseReminderMinutes: minutes,
      responseReclaimMinutes: minutes,
      queueReminderMinutes: minutes,
      aiFollowUpMinutes: minutes,
      aiCloseMinutes: minutes,
    })
    .superRefine((values, context) => {
      if (Number(values.responseReclaimMinutes) <= Number(values.responseReminderMinutes)) {
        context.addIssue({
          code: "custom",
          path: ["responseReclaimMinutes"],
          message: messages.reclaimAfterReminder,
        })
      }
    })
}

/** 读取客服超时时长并显示设置表单。 */
export function ServiceTimeoutsSettings() {
  const { t } = useTranslation("settings")
  const timeouts = useResource(resourceKeys.serviceTimeouts(), () => getServiceTimeouts())
  return (
    <ResourceContent resources={timeouts} errorMessage={t("customerService.timeouts.loadError")}>
      {timeouts.data ? (
        <ServiceTimeoutsForm
          values={{
            responseReminderMinutes: String(timeouts.data.responseReminderMinutes),
            responseReclaimMinutes: String(timeouts.data.responseReclaimMinutes),
            queueReminderMinutes: String(timeouts.data.queueReminderMinutes),
            aiFollowUpMinutes: String(timeouts.data.aiFollowUpMinutes),
            aiCloseMinutes: String(timeouts.data.aiCloseMinutes),
          }}
        />
      ) : null}
    </ResourceContent>
  )
}

/** 维护未回复提醒、未回复回收、队列等待提醒与 AI 跟进、AI 关闭会话时长，修改后自动保存。 */
function ServiceTimeoutsForm({ values }: { values: ServiceTimeoutsFormValues }) {
  const { t } = useTranslation("settings")
  const invalidate = useResourceInvalidator()
  const schema = useMemo(
    () =>
      createServiceTimeoutsSchema({
        minutesInvalid: t("customerService.timeouts.validation.minutesInvalid"),
        reclaimAfterReminder: t("customerService.timeouts.validation.reclaimAfterReminder"),
      }),
    [t],
  )
  const form = useForm<ServiceTimeoutsFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    mode: "onChange",
    defaultValues: values,
  })
  const { submit } = useFormSave({
    form,
    schema,
    autoSave: true,
    save: async (values) => {
      await updateServiceTimeouts({
        responseReminderMinutes: Number(values.responseReminderMinutes),
        responseReclaimMinutes: Number(values.responseReclaimMinutes),
        queueReminderMinutes: Number(values.queueReminderMinutes),
        aiFollowUpMinutes: Number(values.aiFollowUpMinutes),
        aiCloseMinutes: Number(values.aiCloseMinutes),
      })
      void invalidate(resourceKeys.serviceTimeouts())
    },
    errorMessage: t("customerService.timeouts.saveError"),
    errorFields: [...timeoutFields],
    logLabel: "保存客服超时时长",
  })

  return (
    <form
      className="w-full"
      aria-label={t("customerService.timeouts.formLabel")}
      onSubmit={form.handleSubmit(submit)}
      noValidate
    >
      <FieldGroup>
        {timeoutFields.map((name) => (
          <Controller
            key={name}
            name={name}
            control={form.control}
            rules={name === "responseReminderMinutes" ? { deps: ["responseReclaimMinutes"] } : undefined}
            render={({ field, fieldState }) => (
              <Field data-invalid={fieldState.invalid}>
                <FieldLabel htmlFor={field.name} required>
                  {t(`customerService.timeouts.${name}`)}
                </FieldLabel>
                <div className="flex items-center gap-2">
                  <Input
                    {...field}
                    id={field.name}
                    type="number"
                    inputMode="numeric"
                    min={1}
                    step={1}
                    className="w-28"
                    aria-invalid={fieldState.invalid}
                    aria-describedby={`${field.name}-description`}
                    required
                  />
                  <span className="text-sm text-muted-foreground">
                    {t("customerService.timeouts.minutes")}
                  </span>
                </div>
                <FieldDescription id={`${field.name}-description`}>
                  {t(`customerService.timeouts.${name}Description`)}
                </FieldDescription>
              </Field>
            )}
          />
        ))}
      </FieldGroup>
    </form>
  )
}

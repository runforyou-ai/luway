/** 平台设置的积分页：设置每个工作区每天赠送的积分，修改后立即保存。 */
import { useMemo } from "react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { z } from "zod"

import { getPlatformSettings, updatePlatformDailyCreditGrant, type PlatformSettings } from "@/api"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent } from "@/components/resource-content"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"

/** 每日赠送积分的上限。 */
const maxDailyCreditGrant = 1_000_000_000_000

/** 读取平台设置后渲染每日赠送积分表单。 */
export function PlatformCreditsPage() {
  const { t } = useTranslation("platform")
  const settings = useResource(resourceKeys.platformSettings(), (signal) => getPlatformSettings(signal), {
    gcTime: 0,
    refetchOnWindowFocus: false,
  })
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("credits.title")} description={t("credits.description")} />
      <PageContent variant="form">
        <ResourceContent resources={settings} errorMessage={t("credits.loadError")}>
          {settings.data ? <DailyCreditGrantForm settings={settings.data} /> : null}
        </ResourceContent>
      </PageContent>
    </div>
  )
}

/** 每日赠送积分输入框，离开输入框后保存。 */
function DailyCreditGrantForm({ settings }: { settings: PlatformSettings }) {
  const { t } = useTranslation("platform")
  const schema = useMemo(
    () =>
      z.object({
        dailyCreditGrant: z
          .string()
          .trim()
          .refine((value) => /^\d+$/.test(value) && Number(value) <= maxDailyCreditGrant, t("credits.dailyGrantInvalid")),
      }),
    [t],
  )
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    mode: "onBlur",
    defaultValues: { dailyCreditGrant: String(settings.dailyCreditGrant) },
  })
  const { submit } = useFormSave({
    form,
    schema,
    autoSave: true,
    save: (values) => updatePlatformDailyCreditGrant({ dailyCreditGrant: Number(values.dailyCreditGrant.trim()) }),
    savedValues: (saved) => ({ dailyCreditGrant: String(saved.dailyCreditGrant) }),
    errorMessage: t("credits.saveError"),
    errorFields: ["dailyCreditGrant"],
    logLabel: "保存每日赠送积分",
  })

  return (
    <form onSubmit={form.handleSubmit(submit)} noValidate>
      <Controller
        name="dailyCreditGrant"
        control={form.control}
        render={({ field, fieldState }) => (
          <Field data-invalid={fieldState.invalid}>
            <FieldLabel htmlFor="platform-daily-credit-grant" required>
              {t("credits.dailyGrant")}
            </FieldLabel>
            <Input
              {...field}
              id="platform-daily-credit-grant"
              inputMode="numeric"
              autoComplete="off"
              required
              aria-invalid={fieldState.invalid}
            />
            <FieldDescription>{t("credits.dailyGrantHelp")}</FieldDescription>
          </Field>
        )}
      />
    </form>
  )
}

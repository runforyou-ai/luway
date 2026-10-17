/** 企业网页读取设置：选择读取服务并填写凭据，修改后自动保存，可测试读取服务是否可用。 */
import { useEffect, useMemo } from "react"
import { useMutation } from "@tanstack/react-query"
import { LoaderCircleIcon } from "lucide-react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  getWebFetchSettings,
  isApiError,
  testWebFetchService,
  updateWebFetchSettings,
  WebFetchProvider,
  type WebFetchSettings,
} from "@/api"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NativeSelect } from "@/components/ui/native-select"
import {
  createWebFetchSchema,
  type WebFetchFormValues,
} from "@/features/integrations/web/web-fetch-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 读取服务商名称的词条键。 */
type ProviderNameKey = "direct" | "jina" | "firecrawl"

/** 网页读取服务商列表。 */
const providers: { id: WebFetchProvider; name: ProviderNameKey }[] = [
  { id: WebFetchProvider.Direct, name: "direct" },
  { id: WebFetchProvider.Jina, name: "jina" },
  { id: WebFetchProvider.Firecrawl, name: "firecrawl" },
]

/** 网页读取设置：加载与表单。 */
export function WebFetchSettingsSection() {
  const { t } = useTranslation("integrations")
  const settings = useResource(resourceKeys.webFetchSettings(), () => getWebFetchSettings())

  return (
    <ResourceContent resources={[settings]} errorMessage={t("webFetch.loadError")}>
      {settings.data ? <WebFetchForm values={formValues(settings.data)} /> : null}
    </ResourceContent>
  )
}

/** 把网页读取设置转换为表单值。 */
function formValues(settings: WebFetchSettings): WebFetchFormValues {
  return {
    provider: settings.provider || WebFetchProvider.Direct,
    apiKey: settings.apiKey ?? "",
    baseUrl: settings.baseUrl ?? "",
  }
}

/** 维护读取服务与凭据，修改后自动保存。 */
function WebFetchForm({ values }: { values: WebFetchFormValues }) {
  const { t } = useTranslation("integrations")
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const serviceTest = useMutation({
    mutationFn: (tested: WebFetchFormValues) =>
      testWebFetchService({
        provider: tested.provider as WebFetchProvider,
        apiKey: tested.apiKey,
        baseUrl: tested.baseUrl,
      }),
  })
  const testing = serviceTest.isPending
  const schema = useMemo(
    () =>
      createWebFetchSchema({
        apiKeyRequired: t("credentials.apiKeyRequired"),
        baseUrlInvalid: t("webFetch.validation.baseUrlInvalid"),
      }),
    [t],
  )
  const form = useForm<WebFetchFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    mode: "onBlur",
    defaultValues: values,
  })
  const watched = form.watch()
  const { submit, markSaved, mounted } = useFormSave({
    form,
    schema,
    autoSave: true,
    save: async (submitted) => {
      const saved = await updateWebFetchSettings({
        provider: submitted.provider as WebFetchProvider,
        apiKey: submitted.apiKey,
        baseUrl: submitted.baseUrl,
      })
      void invalidate(resourceKeys.webFetchSettings())
      return saved
    },
    savedValues: (saved) => formValues(saved),
    errorMessage: t("webFetch.saveError"),
    errorFields: ["provider", "apiKey", "baseUrl"],
    logLabel: "网页读取设置更新",
    unsaved: !schema.safeParse(watched).success,
  })
  const provider = watched.provider
  const needsCredential = provider === WebFetchProvider.Jina || provider === WebFetchProvider.Firecrawl
  const serializedValues = JSON.stringify(values)

  useEffect(() => {
    if (form.formState.isDirty) return
    const latest = JSON.parse(serializedValues) as WebFetchFormValues
    form.reset(latest)
    markSaved(latest)
  }, [form, serializedValues])

  /** 校验当前填写的内容并用它读取一次固定页面，提示读取服务是否可用。 */
  async function testService() {
    const valid = await form.trigger(["apiKey", "baseUrl"], { shouldFocus: true })
    if (!valid || !mounted.current) return
    const tested = form.getValues()
    const stale = () => JSON.stringify(form.getValues()) !== JSON.stringify(tested)
    serviceTest.mutate(tested, {
      onSuccess: () => {
        if (!stale()) toast.success(t("webFetch.testSuccess"))
      },
      onError: (error) => {
        if (recoverSession(error, navigate) || stale()) return
        console.warn("网页读取服务测试失败", { provider, error })
        toast.error(isApiError(error) ? apiErrorMessage(error, ["provider", "apiKey", "baseUrl"]) : t("webFetch.testError"))
      },
    })
  }

  return (
    <form className="w-full space-y-9" aria-label={t("webFetch.formLabel")} onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup>
        <Controller
          name="provider"
          control={form.control}
          render={({ field }) => (
            <Field>
              <FieldLabel htmlFor="web-fetch-provider">{t("webFetch.provider")}</FieldLabel>
              <NativeSelect
                {...field}
                id="web-fetch-provider"
                onChange={(event) => {
                  field.onChange(event.target.value)
                  // 更换服务商时清空上一家凭据；必填校验留到失焦或测试连接，避免一切换就抢焦点报错。
                  form.setValue("apiKey", "", { shouldDirty: true, shouldValidate: false })
                  form.setValue("baseUrl", "", { shouldDirty: true, shouldValidate: false })
                  form.clearErrors(["apiKey", "baseUrl"])
                }}
              >
                {providers.map(({ id, name }) => (
                  <option key={id} value={id}>
                    {t(`webFetch.providers.${name}`)}
                  </option>
                ))}
              </NativeSelect>
              <FieldDescription>{t("webFetch.providerDescription")}</FieldDescription>
            </Field>
          )}
        />
        {needsCredential ? (
          <FormInputField
            name="apiKey"
            control={form.control}
            label={t("credentials.apiKey")}
            autoComplete="off"
            passwordVisibilityLabels={{ show: t("credentials.showAPIKey"), hide: t("credentials.hideAPIKey") }}
          />
        ) : null}
        {needsCredential ? (
          <Controller
            name="baseUrl"
            control={form.control}
            render={({ field, fieldState }) => (
              <Field data-invalid={fieldState.invalid}>
                <FieldLabel htmlFor="web-fetch-base-url">{t("webFetch.baseUrl")}</FieldLabel>
                <Input {...field} id="web-fetch-base-url" inputMode="url" aria-invalid={fieldState.invalid} />
                <FieldDescription>{t("webFetch.baseUrlDescription")}</FieldDescription>
              </Field>
            )}
          />
        ) : null}
      </FieldGroup>
      <FormActions saving={false} submit={false}>
        <Button type="button" variant="outline" disabled={testing} onClick={() => void testService()}>
          {testing ? <LoaderCircleIcon className="animate-spin" /> : null}
          {testing ? t("connection.testing") : t("connection.test")}
        </Button>
      </FormActions>
    </form>
  )
}

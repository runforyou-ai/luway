/** 企业联网搜索设置：选择搜索服务并填写凭据，修改后自动保存，可测试搜索服务是否可用。 */
import { useEffect, useMemo } from "react"
import { useMutation } from "@tanstack/react-query"
import { LoaderCircleIcon } from "lucide-react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  getWebSearchSettings,
  isApiError,
  testWebSearchService,
  updateWebSearchSettings,
  WebSearchProvider,
  type WebSearchSettings,
} from "@/api"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NativeSelect } from "@/components/ui/native-select"
import {
  createWebSearchSchema,
  type WebSearchFormValues,
} from "@/features/integrations/web-search/web-search-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 搜索服务商名称的词条键。 */
type ProviderNameKey =
  | "tavily"
  | "brave"
  | "exa"
  | "perplexity"
  | "serper"
  | "serpapi"
  | "jina"
  | "firecrawl"
  | "bocha"
  | "aliyunIqs"
  | "baidu"
  | "volcengine"
  | "zhipu"
  | "searxng"

/** 按地区分组的搜索服务商及其名称词条。 */
const providerGroups: {
  group: "global" | "china" | "selfHosted"
  providers: { id: WebSearchProvider; name: ProviderNameKey }[]
}[] = [
  {
    group: "global",
    providers: [
      { id: WebSearchProvider.Tavily, name: "tavily" },
      { id: WebSearchProvider.Brave, name: "brave" },
      { id: WebSearchProvider.Exa, name: "exa" },
      { id: WebSearchProvider.Perplexity, name: "perplexity" },
      { id: WebSearchProvider.Serper, name: "serper" },
      { id: WebSearchProvider.SerpAPI, name: "serpapi" },
      { id: WebSearchProvider.Jina, name: "jina" },
      { id: WebSearchProvider.Firecrawl, name: "firecrawl" },
    ],
  },
  {
    group: "china",
    providers: [
      { id: WebSearchProvider.Bocha, name: "bocha" },
      { id: WebSearchProvider.AliyunIQS, name: "aliyunIqs" },
      { id: WebSearchProvider.Baidu, name: "baidu" },
      { id: WebSearchProvider.Volcengine, name: "volcengine" },
      { id: WebSearchProvider.Zhipu, name: "zhipu" },
    ],
  },
  {
    group: "selfHosted",
    providers: [{ id: WebSearchProvider.SearXNG, name: "searxng" }],
  },
]

/** 读取联网搜索设置并显示设置表单。 */
export function WebSearchSettingsPage() {
  const { t } = useTranslation("integrations")
  const settings = useResource(resourceKeys.webSearchSettings(), () => getWebSearchSettings())

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader title={t("webSearch.title")} description={t("webSearch.description")} />
      <PageContent variant="form">
        <ResourceContent resources={[settings]} errorMessage={t("webSearch.loadError")}>
          {settings.data ? <WebSearchForm values={formValues(settings.data)} /> : null}
        </ResourceContent>
      </PageContent>
    </div>
  )
}

/** 把联网搜索设置转换为表单值，未启用时服务商为空。 */
function formValues(settings: WebSearchSettings): WebSearchFormValues {
  const service = settings.service
  return { provider: service?.provider ?? "", apiKey: service?.apiKey ?? "", baseUrl: service?.baseUrl ?? "" }
}

/** 维护搜索服务与凭据，修改后自动保存。 */
function WebSearchForm({ values }: { values: WebSearchFormValues }) {
  const { t } = useTranslation("integrations")
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const serviceTest = useMutation({
    mutationFn: (tested: WebSearchFormValues) =>
      testWebSearchService({ provider: tested.provider as WebSearchProvider, apiKey: tested.apiKey, baseUrl: tested.baseUrl }),
  })
  const testing = serviceTest.isPending
  const schema = useMemo(
    () =>
      createWebSearchSchema({
        apiKeyRequired: t("credentials.apiKeyRequired"),
        baseUrlRequired: t("webSearch.validation.baseUrlRequired"),
        baseUrlInvalid: t("webSearch.validation.baseUrlInvalid"),
      }),
    [t],
  )
  const form = useForm<WebSearchFormValues>({
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
      const saved = await updateWebSearchSettings({
        service: submitted.provider
          ? { provider: submitted.provider as WebSearchProvider, apiKey: submitted.apiKey, baseUrl: submitted.baseUrl }
          : null,
      })
      void invalidate(resourceKeys.webSearchSettings())
      return saved
    },
    savedValues: (saved) => formValues(saved),
    errorMessage: t("webSearch.saveError"),
    errorFields: ["provider", "apiKey", "baseUrl"],
    logLabel: "联网搜索设置更新",
    // 未填完的配置不会自动保存，离开时提示。
    unsaved: !schema.safeParse(watched).success,
  })
  const provider = watched.provider
  const selfHosted = provider === WebSearchProvider.SearXNG
  const serializedValues = JSON.stringify(values)

  // 设置重新读取后，表单没有未保存的改动时回填最新设置并同步保存基准。
  useEffect(() => {
    if (form.formState.isDirty) return
    const latest = JSON.parse(serializedValues) as WebSearchFormValues
    form.reset(latest)
    markSaved(latest)
  }, [form, serializedValues])

  /** 校验当前填写的内容并用它搜索一次，提示搜索服务是否可用。 */
  async function testService() {
    const valid = await form.trigger(["apiKey", "baseUrl"], { shouldFocus: true })
    if (!valid || !mounted.current) return
    const tested = form.getValues()
    // 测试期间改动了配置时不再提示这次的结果。
    const stale = () => JSON.stringify(form.getValues()) !== JSON.stringify(tested)
    serviceTest.mutate(tested, {
      onSuccess: () => {
        if (!stale()) toast.success(t("webSearch.testSuccess"))
      },
      onError: (error) => {
        if (recoverSession(error, navigate) || stale()) return
        console.warn("搜索服务测试失败", { provider, error })
        toast.error(isApiError(error) ? apiErrorMessage(error, ["provider", "apiKey", "baseUrl"]) : t("webSearch.testError"))
      },
    })
  }

  return (
    <form className="w-full space-y-9" aria-label={t("webSearch.formLabel")} onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup>
        <Controller
          name="provider"
          control={form.control}
          render={({ field }) => (
            <Field>
              <FieldLabel htmlFor="web-search-provider">{t("webSearch.provider")}</FieldLabel>
              <NativeSelect
                {...field}
                id="web-search-provider"
                onChange={(event) => {
                  field.onChange(event.target.value)
                  // 更换服务商时清空上一家的凭据与服务地址，并提示填写新服务商的必填项。
                  form.setValue("apiKey", "", { shouldDirty: true })
                  form.setValue("baseUrl", "", { shouldDirty: true })
                  if (event.target.value !== "") {
                    const required = event.target.value === WebSearchProvider.SearXNG ? "baseUrl" : "apiKey"
                    requestAnimationFrame(() => void form.trigger(required, { shouldFocus: true }))
                  }
                }}
              >
                <option value="">{t("webSearch.notUsed")}</option>
                {providerGroups.map(({ group, providers }) => (
                  <optgroup key={group} label={t(`webSearch.groups.${group}`)}>
                    {providers.map(({ id, name }) => (
                      <option key={id} value={id}>
                        {t(`webSearch.providers.${name}`)}
                      </option>
                    ))}
                  </optgroup>
                ))}
              </NativeSelect>
              <FieldDescription>{t("webSearch.providerDescription")}</FieldDescription>
            </Field>
          )}
        />
        {provider !== "" && !selfHosted ? (
          <FormInputField
            name="apiKey"
            control={form.control}
            label={t("credentials.apiKey")}
            autoComplete="off"
            passwordVisibilityLabels={{ show: t("credentials.showAPIKey"), hide: t("credentials.hideAPIKey") }}
          />
        ) : null}
        {selfHosted ? (
          <Controller
            name="baseUrl"
            control={form.control}
            render={({ field, fieldState }) => (
              <Field data-invalid={fieldState.invalid}>
                <FieldLabel htmlFor="web-search-base-url" required>
                  {t("webSearch.baseUrl")}
                </FieldLabel>
                <Input {...field} id="web-search-base-url" inputMode="url" required aria-invalid={fieldState.invalid} />
                <FieldDescription>{t("webSearch.baseUrlDescription")}</FieldDescription>
              </Field>
            )}
          />
        ) : null}
      </FieldGroup>
      {provider !== "" ? (
        <FormActions saving={false} submit={false}>
          <Button type="button" variant="outline" disabled={testing} onClick={() => void testService()}>
            {testing ? <LoaderCircleIcon className="animate-spin" /> : null}
            {testing ? t("connection.testing") : t("connection.test")}
          </Button>
        </FormActions>
      ) : null}
    </form>
  )
}

/** 平台供应商表单页：新建时品牌取自路由，编辑时边改边存，可测试当前连接配置。 */
import { useEffect, useMemo, useRef, useState } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { Navigate, useNavigate, useParams } from "react-router"
import { toast } from "sonner"

import {
  AIProviderCredentialType,
  createPlatformAIProvider,
  getPlatformAIProvider,
  isApiError,
  listPlatformAIProviders,
  testAIProviderConnection,
  updatePlatformAIProvider,
} from "@/api"
import { FormActions } from "@/components/form/form-actions"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { ModelProviderConnectionFields } from "@/features/integrations/model-services/model-provider-connection-fields"
import { aiProviderBrandConfigs, aiProviderBrandOrder } from "@/features/integrations/model-services/model-provider-brands"
import {
  createAIProviderConnectionSchema,
  type AIProviderConnectionFormValues,
} from "@/features/integrations/model-services/model-provider-schema"
import { platformProviderListPath } from "@/features/settings/deployment/deployment-platform-provider-list-page"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 编辑平台供应商的名称与连接配置；新建后回到列表，品牌创建后不可修改。 */
export function DeploymentPlatformProviderFormPage({ mode }: { mode: "create" | "edit" }) {
  const { t } = useTranslation(["deployment", "integrations", "common"])
  const navigate = useNavigate()
  const { providerId = "", brand: routeBrand = "" } = useParams()
  const invalidateResource = useResourceInvalidator()
  const [testingConnection, setTestingConnection] = useState(false)
  const createBrand = aiProviderBrandOrder.find((brand) => brand === routeBrand) ?? null
  const initialBrand = createBrand ?? aiProviderBrandOrder[0]
  const schema = useMemo(
    () =>
      createAIProviderConnectionSchema({
        brandInvalid: t("integrations:modelServices.validation.brandInvalid"),
        credentialTypeInvalid: t("integrations:modelServices.validation.credentialTypeInvalid"),
        nameRequired: t("integrations:modelServices.validation.nameRequired"),
        nameTooLong: t("integrations:modelServices.validation.nameTooLong"),
        apiKeyRequired: t("integrations:credentials.apiKeyRequired"),
        apiKeyTooLong: t("integrations:modelServices.validation.apiKeyTooLong"),
        apiUrlRequired: t("integrations:modelServices.validation.apiUrlRequired"),
        apiUrlInvalid: t("integrations:modelServices.validation.apiUrlInvalid"),
      }),
    [t],
  )
  const form = useForm<AIProviderConnectionFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    // 编辑时离开字段即校验以便自动保存；新建时等提交再校验。
    mode: mode === "edit" ? "onBlur" : "onSubmit",
    defaultValues: {
      brand: initialBrand,
      name: t(aiProviderBrandConfigs[initialBrand].nameKey, { ns: "integrations" }),
      credentialType: AIProviderCredentialType.AIProviderCredentialTypeAPIKey,
      apiKey: "",
      apiUrl: aiProviderBrandConfigs[initialBrand].defaultAPIURL,
    },
  })
  const detail = useResource(resourceKeys.platformAIProvider(providerId), (signal) => getPlatformAIProvider(providerId, signal), {
    enabled: mode === "edit",
  })
  const providers = useResource(resourceKeys.platformAIProviders(), (signal) => listPlatformAIProviders(signal), {
    enabled: mode === "create",
  })

  /** 新建时把默认名称设为不与已有平台供应商重名的品牌名。 */
  useEffect(() => {
    if (mode !== "create" || !providers.data || form.getFieldState("name").isDirty) return
    const brandName = t(aiProviderBrandConfigs[initialBrand].nameKey, { ns: "integrations" })
    const taken = new Set(providers.data.map((item) => item.name.trim().toLocaleLowerCase()))
    let name = brandName
    for (let suffix = 2; taken.has(name.toLocaleLowerCase()); suffix += 1) {
      name = `${brandName} ${suffix}`
    }
    form.setValue("name", name)
  }, [form, initialBrand, mode, providers.data, t])

  const initializedDetail = useRef<string | null>(null)
  /** 详情就绪后回填表单。 */
  useEffect(() => {
    const provider = detail.data
    if (!provider) return
    if (initializedDetail.current === providerId && form.formState.isDirty) return
    initializedDetail.current = providerId
    const values = {
      brand: provider.brand,
      name: provider.name,
      credentialType: provider.credentialType,
      apiKey: provider.apiKey,
      apiUrl: provider.apiUrl,
    }
    form.reset(values)
    markSaved(values)
  }, [form, detail.data, providerId])

  /** 使用当前未保存的地址和密钥测试连接。 */
  async function testConnection() {
    if (testingConnection || form.formState.isSubmitting) return
    const valid = await form.trigger(["brand", "credentialType", "apiKey", "apiUrl"], { shouldFocus: true })
    if (!valid || !mounted.current) return
    const { brand, credentialType, apiKey, apiUrl } = form.getValues()
    setTestingConnection(true)
    try {
      await testAIProviderConnection({ brand, credentialType, apiKey, apiUrl })
      if (!mounted.current) return
      toast.success(t("integrations:modelServices.form.testSuccess"))
    } catch (requestError) {
      if (!mounted.current) return
      if (recoverSession(requestError, navigate)) return
      console.warn("平台供应商连接测试失败", { brand, error: requestError })
      toast.error(
        isApiError(requestError)
          ? apiErrorMessage(requestError, ["brand", "credentialType", "apiKey", "apiUrl"])
          : t("integrations:modelServices.form.testError"),
      )
    } finally {
      if (mounted.current) setTestingConnection(false)
    }
  }

  // 编辑已有供应商时边改边存，新建由底部按钮提交并回到列表。
  const { submit, mounted, markSaved } = useFormSave({
    form,
    schema,
    autoSave: mode === "edit",
    save: async (values) => {
      const input = { name: values.name, credentialType: values.credentialType, apiKey: values.apiKey, apiUrl: values.apiUrl }
      if (mode === "create") {
        await createPlatformAIProvider({ ...input, brand: values.brand })
      } else {
        await updatePlatformAIProvider(providerId, input)
        void invalidateResource(resourceKeys.platformAIProvider(providerId))
      }
      void invalidateResource(resourceKeys.platformAIProviders())
      void invalidateResource(resourceKeys.platformAIModels())
    },
    onSubmitted: () => {
      toast.success(t("platformProviders.form.createSuccess"))
      navigate(platformProviderListPath)
    },
    errorMessage: t("platformProviders.form.saveError"),
    errorFields: ["brand", "name", "credentialType", "apiKey", "apiUrl"],
    logLabel: "平台供应商保存",
  })

  if (mode === "create" && !createBrand) return <Navigate to={platformProviderListPath} replace />

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader
        title={t(mode === "create" ? "platformProviders.form.createTitle" : "platformProviders.form.editTitle")}
        description={t(mode === "create" ? "platformProviders.form.createDescription" : "platformProviders.form.editDescription")}
        backTo={mode === "edit" ? platformProviderListPath : undefined}
      />
      <PageContent variant="form">
        <ResourceContent resources={mode === "edit" ? detail : []} errorMessage={t("platformProviders.form.loadError")}>
          <form className="w-full space-y-9" onSubmit={form.handleSubmit(submit)} noValidate>
            <ModelProviderConnectionFields form={form} mode={mode} />
            <FormActions
              saving={form.formState.isSubmitting}
              disabled={testingConnection}
              cancelTo={platformProviderListPath}
              submit={mode === "create"}
            >
              <Button
                type="button"
                variant="outline"
                disabled={testingConnection || form.formState.isSubmitting}
                onClick={() => void testConnection()}
              >
                {testingConnection ? <LoaderCircleIcon className="animate-spin" /> : null}
                {testingConnection ? t("integrations:connection.testing") : t("integrations:connection.test")}
              </Button>
            </FormActions>
          </form>
        </ResourceContent>
      </PageContent>
    </div>
  )
}

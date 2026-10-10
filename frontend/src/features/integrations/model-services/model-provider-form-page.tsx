/** 模型服务供应商表单页。 */
import { useEffect, useMemo, useRef } from "react"
import { useMutation } from "@tanstack/react-query"
import { LoaderCircleIcon } from "lucide-react"
import {
  useFieldArray,
  useForm,
} from "react-hook-form"
import { useTranslation } from "react-i18next"
import { Navigate, useNavigate, useParams } from "react-router"
import { toast } from "sonner"

import {
  AIModelType,
  AIProviderBrand,
  AIProviderCredentialType,
  createAIProvider,
  getAIProvider,
  isApiError,
  listAIProviders,
  testAIProviderConnection,
  updateAIProvider,
} from "@/api"
import { FormActions } from "@/components/form/form-actions"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { useAIModelSchemaMessages } from "@/components/model-provider/use-ai-model-schema-messages"
import {
  aiProviderBrandConfigs,
  aiProviderBrandOrder,
} from "@/components/model-provider/model-provider-brands"
import {
  modelFormValue,
  rollbackModels,
} from "@/features/integrations/model-services/model-provider-model-values"
import {
  createAIProviderSchema,
  parseTokenCount,
  type AIModelFormValues,
  type AIProviderFormValues,
} from "@/components/model-provider/model-provider-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"

import { ModelProviderModelEditor } from "./model-provider-model-editor"
import { ModelProviderConnectionFields } from "@/components/model-provider/model-provider-connection-fields"

const listPath = "/settings/model-services"

/** 编辑供应商连接和模型目录；新建时品牌取自路由，品牌创建后不可修改。 */
export function ModelProviderFormPage({ mode }: { mode: "create" | "edit" }) {
  const { t } = useTranslation(["integrations", "common"])
  const navigate = useNavigate()
  const reportError = useRequestErrorReporter()
  const { providerId = "", brand: routeBrand = "" } = useParams()
  const invalidateResource = useResourceInvalidator()
  const connectionTest = useMutation({ mutationFn: (input: Parameters<typeof testAIProviderConnection>[0]) => testAIProviderConnection(input) })
  const testingConnection = connectionTest.isPending
  const createBrand = aiProviderBrandOrder.find((brand) => brand === routeBrand) ?? null
  const initialBrand = createBrand ?? AIProviderBrand.DeepSeek
  const modelMessages = useAIModelSchemaMessages()
  const schema = useMemo(
    () =>
      createAIProviderSchema({
        ...modelMessages,
        apiKeyRequired: t("credentials.apiKeyRequired"),
        apiUrlInvalid: t("modelServices.validation.apiUrlInvalid"),
        modelsRequired: t("modelServices.validation.modelsRequired"),
      }),
    [t, modelMessages],
  )
  const form = useForm<AIProviderFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    // 编辑时离开字段即校验以便自动保存，新建时只在提交时校验。
    mode: mode === "edit" ? "onBlur" : "onSubmit",
    defaultValues: {
      brand: initialBrand,
      name: t(aiProviderBrandConfigs[initialBrand].nameKey),
      credentialType: AIProviderCredentialType.APIKey,
      apiKey: "",
      apiUrl: aiProviderBrandConfigs[initialBrand].defaultAPIURL,
      models: [],
    },
  })
  const modelFields = useFieldArray({
    control: form.control,
    name: "models",
  })

  const detail = useResource(resourceKeys.aiProvider(providerId), () => getAIProvider(providerId), {
    enabled: mode === "edit",
  })
  const provider = detail.data
  // 服务端最近一次确认的模型目录，目录改动被服务端拒绝时恢复到该目录。
  const savedModels = useRef<AIModelFormValues[]>([])
  // 新增模型保存后由服务端分配的编号，按表单内标识索引。
  const savedModelIDs = useRef(new Map<string, string>())
  const providers = useResource(resourceKeys.aiProviders(), () => listAIProviders(), {
    enabled: mode === "create",
  })

  // 新建时把默认名称设为不与已有供应商重名的品牌名。
  useEffect(() => {
    if (mode !== "create" || !providers.data || form.getFieldState("name").isDirty) return
    const brandName = t(aiProviderBrandConfigs[initialBrand].nameKey)
    const taken = new Set(
      providers.data.providers.map((item) => item.name.trim().toLocaleLowerCase()),
    )
    let name = brandName
    for (let suffix = 2; taken.has(name.toLocaleLowerCase()); suffix += 1) {
      name = `${brandName} ${suffix}`
    }
    form.setValue("name", name)
  }, [form, initialBrand, mode, providers.data, t])

  const initializedDetail = useRef<string | null>(null)
  // 详情就绪后回填供应商表单。
  useEffect(() => {
    if (!provider) return
    if (initializedDetail.current === providerId && form.formState.isDirty) return
    initializedDetail.current = providerId
    savedModels.current = provider.models.map(modelFormValue)
    const values = {
      brand: provider.brand,
      name: provider.name,
      credentialType: provider.credentialType,
      apiKey: provider.apiKey,
      apiUrl: provider.apiUrl,
      models: provider.models.map(modelFormValue),
    }
    form.reset(values)
    markSaved(values)
  }, [form, provider])

  /** 为尚无编号的模型按表单内标识补上保存后分配的编号。 */
  function withSavedModelIDs(models: AIModelFormValues[]) {
    return models.map((model) => ({
      ...model,
      id: model.id || savedModelIDs.current.get(model.key) || "",
    }))
  }

  /** 使用当前未保存的地址和密钥测试模型服务连接。 */
  async function testConnection() {
    const valid = await form.trigger(["brand", "credentialType", "apiKey", "apiUrl"], {
      shouldFocus: true,
    })
    if (!valid || !mounted.current) return
    const { brand, credentialType, apiKey, apiUrl } = form.getValues()
    connectionTest.mutate({ brand, credentialType, apiKey, apiUrl }, {
      onSuccess: () => toast.success(t("modelServices.form.testSuccess")),
      onError: (requestError) => reportError(requestError, {
        log: "模型服务连接测试",
        context: { brand },
        fallback: t("modelServices.form.testError"),
        fields: ["brand", "credentialType", "apiKey", "apiUrl"],
      }),
    })
  }

  // 创建或保存模型服务供应商：编辑已有供应商时边改边存，新建仍由底部按钮提交并跳回列表。
  const { submit, mounted, markSaved } = useFormSave({
    form,
    schema,
    autoSave: mode === "edit",
    save: async (values) => {
      const input = {
        name: values.name,
        credentialType: values.credentialType,
        apiKey: values.apiKey,
        apiUrl: values.apiUrl,
        models: withSavedModelIDs(values.models).map((model) => ({
          id: model.id,
          identifier: model.identifier,
          name: model.name,
          type: model.type,
          inputModalities: model.inputModalities,
          contextWindow: parseTokenCount(model.contextWindow)!,
          maxOutputTokens:
            model.type === AIModelType.Chat
              ? parseTokenCount(model.maxOutputTokens)!
              : 0,
        })),
      }
      if (mode === "create") {
        await createAIProvider({ ...input, brand: values.brand })
      } else {
        try {
          const saved = await updateAIProvider(providerId, input)
          // 服务端按请求顺序返回模型目录。
          savedModels.current = values.models.map((model, index) => {
            savedModelIDs.current.set(model.key, saved.models[index].id)
            return { ...model, id: saved.models[index].id }
          })
        } catch (error) {
          // 服务端拒绝模型目录时撤回本次请求的目录改动，其余字段和请求后的编辑保留。
          if (isApiError(error) && error.fields.models) {
            modelFields.replace(
              rollbackModels(savedModels.current, values.models, form.getValues("models")),
            )
          }
          throw error
        }
        void invalidateResource(resourceKeys.aiProvider(providerId))
      }
      void invalidateResource(resourceKeys.aiProviders())
    },
    // 自动保存后用服务端分配的编号回填新增模型。
    savedValues: (_, values) => ({ ...values, models: withSavedModelIDs(values.models) }),
    onSubmitted: () => {
      toast.success(t("modelServices.form.createSuccess"))
      navigate(listPath)
    },
    // 已保存的供应商目录不合法时暂不保存，离开页面前提示未保存的改动。
    unsaved: mode === "edit" && Boolean(form.formState.errors.models),
    errorMessage: t("modelServices.form.saveError"),
    errorFields: ["brand", "name", "credentialType", "apiKey", "apiUrl", "models"],
    logLabel: "模型服务供应商保存",
  })

  const title =
    mode === "create"
      ? t("modelServices.form.createTitle")
      : t("modelServices.form.editTitle")
  if (mode === "create" && !createBrand) return <Navigate to={listPath} replace />

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader
        title={title}
        description={t(
          mode === "create"
            ? "modelServices.form.createDescription"
            : "modelServices.form.editDescription",
        )}
        backTo={mode === "edit" ? listPath : undefined}
      />
      <PageContent variant="form">
        <ResourceContent
          resources={mode === "edit" ? detail : []}
          errorMessage={t("modelServices.form.loadError")}
        >
          <form
            className="w-full space-y-9"
            onSubmit={form.handleSubmit(submit)}
            noValidate
          >
            <ModelProviderConnectionFields form={form} mode={mode} />

            <ModelProviderModelEditor form={form} modelFields={modelFields} mode={mode} />
            <FormActions
              saving={form.formState.isSubmitting}
              disabled={testingConnection}
              cancelTo={listPath}
              submit={mode === "create"}
            >
              <Button
                type="button"
                variant="outline"
                disabled={testingConnection || form.formState.isSubmitting}
                onClick={() => void testConnection()}
              >
                {testingConnection ? <LoaderCircleIcon className="animate-spin" /> : null}
                {testingConnection
                  ? t("connection.testing")
                  : t("connection.test")}
              </Button>
            </FormActions>
          </form>
        </ResourceContent>
      </PageContent>

    </div>
  )
}

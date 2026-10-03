/** 平台模型表单页：维护模型属性、积分价格与按尝试顺序排列的来源，新建后回到列表，编辑时边改边存。 */
import { useEffect, useMemo, useRef, useState } from "react"
import { ArrowDownIcon, ArrowUpIcon, ListIcon, PlusIcon, Trash2Icon } from "lucide-react"
import { Controller, useFieldArray, useForm, useWatch } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate, useParams } from "react-router"
import { toast } from "sonner"

import {
  AIModelType,
  createPlatformAIModel,
  getPlatformAIModel,
  listPlatformAIProviders,
  updatePlatformAIModel,
  type AIProviderModelData,
} from "@/api"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { SwitchField } from "@/components/form/switch-field"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldRequiredMark,
  FieldSet,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NativeSelect } from "@/components/ui/native-select"
import { useAIModelSchemaMessages } from "@/features/integrations/model-services/model-edit-dialog"
import { formatTokenCount } from "@/features/integrations/model-services/model-provider-model-values"
import { parseTokenCount } from "@/features/integrations/model-services/model-provider-schema"
import {
  modelInputModalityNameKeys,
  modelInputModalityOrder,
  modelTypeNameKeys,
  modelTypeOrder,
} from "@/features/integrations/model-services/model-service-options"
import { platformModelListPath } from "@/features/settings/platform/platform-model-list-page"
import { platformProviderListPath } from "@/features/settings/platform/platform-provider-list-page"
import { PlatformModelPickerDialog } from "@/features/settings/platform/platform-model-picker-dialog"
import {
  createPlatformModelSchema,
  platformModelFormValue,
  platformModelPrice,
  priceFieldsByType,
  type PlatformModelFormValues,
} from "@/features/settings/platform/platform-model-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"

/** 编辑平台模型；来源顺序即尝试顺序，可从供应商可提供的模型中选择标识，模型名称为空时一并带入模型属性。 */
export function PlatformModelFormPage({ mode }: { mode: "create" | "edit" }) {
  const { t } = useTranslation(["platform", "integrations", "common"])
  const navigate = useNavigate()
  const { modelId = "" } = useParams()
  const invalidateResource = useResourceInvalidator()
  const modelMessages = useAIModelSchemaMessages()
  const schema = useMemo(
    () =>
      createPlatformModelSchema({
        ...modelMessages,
        providerRequired: t("platformModels.validation.providerRequired"),
        routesRequired: t("platformModels.validation.routesRequired"),
        routeDuplicate: t("platformModels.validation.routeDuplicate"),
        priceInvalid: t("platformModels.validation.priceInvalid"),
      }),
    [t, modelMessages],
  )
  const form = useForm<PlatformModelFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    // 编辑时离开字段即校验以便自动保存；新建时等提交再校验。
    mode: mode === "edit" ? "onBlur" : "onSubmit",
    defaultValues: {
      name: "",
      type: AIModelType.AIModelTypeChat,
      inputModalities: [],
      contextWindow: "",
      maxOutputTokens: "",
      priced: true,
      inputPrice: "",
      outputPrice: "",
      requestPrice: "",
      // 新建时预置一条来源，供应商与模型标识的必填提示落在该行控件上。
      routes: [{ key: crypto.randomUUID(), id: "", providerId: "", identifier: "", enabled: true }],
    },
  })
  const routes = useFieldArray({ control: form.control, name: "routes", keyName: "fieldKey" })
  const watchedRoutes = useWatch({ control: form.control, name: "routes" })
  const modelType = useWatch({ control: form.control, name: "type" })
  const isChat = modelType === AIModelType.AIModelTypeChat
  const priced = useWatch({ control: form.control, name: "priced" })
  const [pickingRoute, setPickingRoute] = useState<number | null>(null)
  const providers = useResource(resourceKeys.platformAIProviders(), (signal) => listPlatformAIProviders(signal))
  const detail = useResource(resourceKeys.platformAIModel(modelId), (signal) => getPlatformAIModel(modelId, signal), {
    enabled: mode === "edit",
  })
  // 新增来源保存后由服务端分配的编号，按表单内标识索引。
  const savedRouteIDs = useRef(new Map<string, string>())

  const initializedDetail = useRef<string | null>(null)
  /** 详情就绪后回填表单。 */
  useEffect(() => {
    if (!detail.data) return
    if (initializedDetail.current === modelId && form.formState.isDirty) return
    initializedDetail.current = modelId
    const values = platformModelFormValue(detail.data)
    form.reset(values)
    markSaved(values)
  }, [form, detail.data, modelId])

  /** 为尚无编号的来源按表单内标识补上保存后分配的编号。 */
  function withSavedRouteIDs(values: PlatformModelFormValues) {
    return {
      ...values,
      routes: values.routes.map((route) => ({ ...route, id: route.id || savedRouteIDs.current.get(route.key) || "" })),
    }
  }

  /** 把选中的上游模型标识写入来源；模型名称为空时带入该模型的属性。 */
  function pickModel(index: number, model: AIProviderModelData) {
    form.setValue(`routes.${index}.identifier`, model.identifier, { shouldDirty: true, shouldValidate: true })
    if (form.getValues("name").trim() === "") {
      form.setValue("name", model.name || model.identifier, { shouldDirty: true })
      if (model.type) form.setValue("type", model.type, { shouldDirty: true })
      if (model.inputModalities.length > 0) form.setValue("inputModalities", model.inputModalities, { shouldDirty: true })
      if (model.contextWindow > 0) form.setValue("contextWindow", formatTokenCount(model.contextWindow), { shouldDirty: true })
      if (model.maxOutputTokens > 0) form.setValue("maxOutputTokens", formatTokenCount(model.maxOutputTokens), { shouldDirty: true })
    }
    setPickingRoute(null)
  }

  // 编辑已有模型时边改边存，新建由底部按钮提交并回到列表。
  const { submit, markSaved } = useFormSave({
    form,
    schema,
    autoSave: mode === "edit",
    save: async (values) => {
      const submitted = withSavedRouteIDs(values)
      const input = {
        name: submitted.name,
        type: submitted.type,
        inputModalities: submitted.inputModalities,
        contextWindow: parseTokenCount(submitted.contextWindow)!,
        maxOutputTokens: submitted.type === AIModelType.AIModelTypeChat ? parseTokenCount(submitted.maxOutputTokens)! : 0,
        price: platformModelPrice(submitted),
        routes: submitted.routes.map((route) => ({
          id: route.id,
          providerId: route.providerId,
          identifier: route.identifier,
          enabled: route.enabled,
        })),
      }
      if (mode === "create") {
        await createPlatformAIModel(input)
      } else {
        const saved = await updatePlatformAIModel(modelId, input)
        // 服务端按请求顺序返回来源。
        submitted.routes.forEach((route, index) => savedRouteIDs.current.set(route.key, saved.routes[index].id))
        void invalidateResource(resourceKeys.platformAIModel(modelId))
      }
      void invalidateResource(resourceKeys.platformAIModels())
      void invalidateResource(resourceKeys.platformAIProviders())
    },
    // 自动保存后用服务端分配的编号回填新增来源。
    savedValues: (_, values) => withSavedRouteIDs(values),
    onSubmitted: () => {
      toast.success(t("platformModels.form.createSuccess"))
      navigate(platformModelListPath)
    },
    errorMessage: t("platformModels.form.saveError"),
    errorFields: ["name", "type", "inputModalities", "contextWindow", "maxOutputTokens", "inputPrice", "outputPrice", "requestPrice", "routes"],
    logLabel: "平台模型保存",
  })

  const providerOptions = providers.data ?? []

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader
        title={t(mode === "create" ? "platformModels.form.createTitle" : "platformModels.form.editTitle")}
        description={t(mode === "create" ? "platformModels.form.createDescription" : "platformModels.form.editDescription")}
        backTo={mode === "edit" ? platformModelListPath : undefined}
      />
      <PageContent variant="form">
        <ResourceContent
          resources={mode === "edit" ? [detail, providers] : [providers]}
          errorMessage={t("platformModels.form.loadError")}
        >
          <form className="w-full space-y-9" onSubmit={form.handleSubmit(submit)} noValidate>
            <FieldGroup>
              <FormInputField
                name="name"
                id="platform-model-name"
                control={form.control}
                label={t("platformModels.form.name")}
                maxLength={200}
                autoComplete="off"
                autoFocus={mode === "create"}
              />
              <Controller
                name="type"
                control={form.control}
                render={({ field, fieldState }) => (
                  <Field data-invalid={fieldState.invalid}>
                    <FieldLabel htmlFor="platform-model-type" required>
                      {t("integrations:modelServices.models.columns.type")}
                    </FieldLabel>
                    <NativeSelect {...field} id="platform-model-type" required aria-invalid={fieldState.invalid}>
                      {modelTypeOrder.map((type) => (
                        <option key={type} value={type}>
                          {t(modelTypeNameKeys[type], { ns: "integrations" })}
                        </option>
                      ))}
                    </NativeSelect>
                  </Field>
                )}
              />
              <Field>
                <FieldLabel id="platform-model-input-modalities" required>
                  {t("integrations:modelServices.models.columns.inputModalities")}
                </FieldLabel>
                <div role="group" aria-labelledby="platform-model-input-modalities" className="flex flex-wrap gap-x-5 gap-y-2">
                  {modelInputModalityOrder.map((modality) => (
                    <label key={modality} className="inline-flex items-center gap-2 text-sm">
                      <input {...form.register("inputModalities")} type="checkbox" value={modality} className="size-4 accent-primary" />
                      {t(modelInputModalityNameKeys[modality], { ns: "integrations" })}
                    </label>
                  ))}
                </div>
              </Field>
              <div className="grid gap-4 sm:grid-cols-2">
                <FormInputField
                  name="contextWindow"
                  id="platform-model-context-window"
                  control={form.control}
                  label={t("integrations:modelServices.models.columns.contextWindow")}
                  inputMode="decimal"
                  autoComplete="off"
                />
                {isChat ? (
                  <FormInputField
                    name="maxOutputTokens"
                    id="platform-model-max-output-tokens"
                    control={form.control}
                    label={t("integrations:modelServices.models.columns.maxOutputTokens")}
                    inputMode="decimal"
                    autoComplete="off"
                  />
                ) : null}
              </div>
            </FieldGroup>

            <FieldSet className="grid gap-3">
              <FieldLegend>{t("platformModels.form.price")}</FieldLegend>
              <Controller
                name="priced"
                control={form.control}
                render={({ field }) => (
                  <SwitchField
                    id="platform-model-priced"
                    name={field.name}
                    label={t("platformModels.form.priced")}
                    description={t(field.value ? "platformModels.form.pricedHelp" : "platformModels.form.unpricedHelp")}
                    checked={field.value}
                    onBlur={field.onBlur}
                    onCheckedChange={field.onChange}
                    ref={field.ref}
                  />
                )}
              />
              {priced ? (
                <div className="grid gap-4 sm:grid-cols-3">
                  {priceFieldsByType[modelType].map((name) => (
                    <FormInputField
                      key={name}
                      name={name}
                      id={`platform-model-${name}`}
                      control={form.control}
                      label={t(`platformModels.form.${name}`)}
                      inputMode="numeric"
                      autoComplete="off"
                      required
                    />
                  ))}
                </div>
              ) : null}
            </FieldSet>

            <FieldSet className="grid gap-3">
              <FieldLegend>
                {t("platformModels.form.routes")} <FieldRequiredMark />
              </FieldLegend>
              <FieldDescription>{t("platformModels.form.routesHelp")}</FieldDescription>
              {providerOptions.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  <Button type="button" variant="link" className="h-auto p-0" onClick={() => navigate(platformProviderListPath)}>
                    {t("platformModels.form.noProviders")}
                  </Button>
                </p>
              ) : null}
              <ol className="divide-y border-y">
                {routes.fields.map((route, index) => (
                  <li key={route.fieldKey} className="flex flex-wrap items-center gap-2 py-3">
                    <span className="w-5 shrink-0 text-center text-xs text-muted-foreground tabular-nums">{index + 1}</span>
                    <Controller
                      name={`routes.${index}.providerId`}
                      control={form.control}
                      render={({ field, fieldState }) => (
                        <NativeSelect
                          {...field}
                          id={`platform-model-route-${index}-provider`}
                          aria-label={t("platformModels.form.provider")}
                          aria-invalid={fieldState.invalid}
                          required
                          className="w-44 shrink-0"
                        >
                          <option value="" disabled>
                            {t("platformModels.form.provider")}
                          </option>
                          {providerOptions.map((provider) => (
                            <option key={provider.id} value={provider.id}>
                              {provider.name}
                            </option>
                          ))}
                        </NativeSelect>
                      )}
                    />
                    <Controller
                      name={`routes.${index}.identifier`}
                      control={form.control}
                      render={({ field, fieldState }) => (
                        <Input
                          {...field}
                          id={`platform-model-route-${index}-identifier`}
                          aria-label={t("platformModels.form.identifier")}
                          aria-invalid={fieldState.invalid}
                          required
                          maxLength={200}
                          autoComplete="off"
                          className="min-w-40 flex-1 font-mono"
                        />
                      )}
                    />
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      aria-label={t("platformModels.form.pickModel")}
                      title={t("platformModels.form.pickModel")}
                      disabled={!watchedRoutes?.[index]?.providerId}
                      onClick={() => setPickingRoute(index)}
                    >
                      <ListIcon />
                    </Button>
                    <label className="inline-flex items-center gap-2 text-sm">
                      <input
                        {...form.register(`routes.${index}.enabled`)}
                        type="checkbox"
                        className="size-4 accent-primary"
                      />
                      {t("platformModels.form.enabled")}
                    </label>
                    <div className="ml-auto flex items-center">
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        aria-label={t("platformModels.form.moveUp")}
                        title={t("platformModels.form.moveUp")}
                        disabled={index === 0}
                        onClick={() => routes.move(index, index - 1)}
                      >
                        <ArrowUpIcon />
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        aria-label={t("platformModels.form.moveDown")}
                        title={t("platformModels.form.moveDown")}
                        disabled={index === routes.fields.length - 1}
                        onClick={() => routes.move(index, index + 1)}
                      >
                        <ArrowDownIcon />
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        aria-label={t("platformModels.form.removeRoute")}
                        title={t("platformModels.form.removeRoute")}
                        disabled={routes.fields.length === 1}
                        onClick={() => routes.remove(index)}
                      >
                        <Trash2Icon />
                      </Button>
                    </div>
                  </li>
                ))}
              </ol>
              <div>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={providerOptions.length === 0}
                  onClick={() =>
                    routes.append({
                      key: crypto.randomUUID(),
                      id: "",
                      providerId: providerOptions.length === 1 ? providerOptions[0].id : "",
                      identifier: "",
                      enabled: true,
                    })
                  }
                >
                  <PlusIcon />
                  {t("platformModels.form.addRoute")}
                </Button>
              </div>
            </FieldSet>

            <FormActions saving={form.formState.isSubmitting} cancelTo={platformModelListPath} submit={mode === "create"} />
          </form>
        </ResourceContent>
      </PageContent>

      <PlatformModelPickerDialog
        providerId={pickingRoute === null ? "" : (watchedRoutes?.[pickingRoute]?.providerId ?? "")}
        onOpenChange={(open) => {
          if (!open) setPickingRoute(null)
        }}
        onPick={(model) => {
          if (pickingRoute !== null) pickModel(pickingRoute, model)
        }}
      />
    </div>
  )
}

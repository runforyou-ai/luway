/** 业务系统的连接表单：名称、传输方式与对应的连接配置、认证方式与凭据、身份请求头。 */
import { useEffect, useId, useMemo, useRef } from "react"
import { useMutation } from "@tanstack/react-query"
import { XIcon } from "lucide-react"
import { Controller, useFieldArray, useForm, useWatch, type UseFormReturn } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  BusinessSystemCredentialKind,
  BusinessSystemTransport,
  ContextValue,
  businessSystemCredentialKinds,
  businessSystemTransports,
  contextValues,
  createBusinessSystem,
  mcpServerTypes,
  testBusinessSystemConnection,
  updateBusinessSystem,
  type BusinessSystem,
} from "@/api"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NativeSelect } from "@/components/ui/native-select"
import { Textarea } from "@/components/ui/textarea"
import {
  businessSystemConnection,
  businessSystemFormValues,
  createBusinessSystemSchema,
  specSources,
  type BusinessSystemFormValues,
} from "@/features/integrations/business-systems/business-system-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { zodResolver } from "@/lib/zod-resolver"

/** 服务端可能回报校验错误的字段。 */
const errorFields = [
  "name", "transport", "url", "serverType", "specUrl", "spec", "baseUrl",
  "credential", "credentialHeaderName", "credentialToken", "credentialUsername", "headerBindings",
]

/** 连接测试前校验的连接与凭据字段。 */
const connectionFields = [
  "url", "serverType", "specUrl", "spec", "baseUrl", "credentialHeaderName", "credentialToken", "credentialUsername",
] as const

/** 新建时提交并回到 listPath，编辑时边改边存；system 为编辑中的业务系统，新建时为空。 */
export function BusinessSystemConnectionForm({
  system,
  listPath,
}: {
  system: BusinessSystem | null
  listPath: string
}) {
  const { t } = useTranslation(["integrations", "common"])
  const navigate = useNavigate()
  const reportError = useRequestErrorReporter()
  const invalidateResource = useResourceInvalidator()
  const connectionTest = useMutation({
    mutationFn: (values: BusinessSystemFormValues) => testBusinessSystemConnection(businessSystemConnection(values)),
  })
  const testing = connectionTest.isPending
  const editing = system !== null
  const schema = useMemo(
    () =>
      createBusinessSystemSchema({
        urlRequired: t("businessSystem.validation.urlRequired"),
        urlTooLong: t("businessSystem.validation.urlTooLong"),
        urlInvalid: t("businessSystem.validation.urlInvalid"),
        specRequired: t("businessSystem.validation.specRequired"),
        credentialRequired: t("businessSystem.validation.credentialRequired"),
        headerNameInvalid: t("businessSystem.validation.headerNameInvalid"),
        headerNameDuplicate: t("businessSystem.validation.headerNameDuplicate"),
        headerNameConflict: t("businessSystem.validation.headerNameConflict"),
      }),
    [t],
  )
  const form = useForm<BusinessSystemFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    // 编辑时离开字段即校验以便自动保存，新建时只在提交时校验。
    mode: editing ? "onBlur" : "onSubmit",
    defaultValues: businessSystemFormValues(system),
  })
  const [transport, specSource, credentialKind, credentialHeaderName] = useWatch({
    control: form.control,
    name: ["transport", "specSource", "credentialKind", "credentialHeaderName"],
  })
  // 认证请求头名称变化后重新校验与之同名或已有提示的身份请求头，冲突提示显示在对应行，解除冲突后清除提示。
  const checkedHeaderName = useRef(credentialHeaderName)
  useEffect(() => {
    if (checkedHeaderName.current === credentialHeaderName) return
    checkedHeaderName.current = credentialHeaderName
    const name = credentialHeaderName.trim().toLowerCase()
    const rows = form.getValues("headerBindings").flatMap((binding, index) => {
      const field = `headerBindings.${index}.header` as const
      return binding.header.trim().toLowerCase() === name || form.getFieldState(field).invalid ? [field] : []
    })
    if (rows.length) void form.trigger(rows)
  }, [form, credentialHeaderName])
  const mcp = transport === BusinessSystemTransport.MCP

  /** 测试当前未保存的连接配置。 */
  async function testConnection() {
    if (!(await form.trigger([...connectionFields], { shouldFocus: true }))) return
    connectionTest.mutate(form.getValues(), {
      onSuccess: () => toast.success(t("businessSystem.connection.success")),
      onError: (error) => reportError(error, { fallback: t("businessSystem.connection.error"), fields: errorFields }),
    })
  }

  // 创建或保存业务系统，保存时由服务端重新读取工具目录；未填写接口根地址时回填服务端取自文档的地址。
  const { submit } = useFormSave({
    form,
    schema,
    autoSave: editing,
    save: async (values) => {
      const input = { name: values.name, headerBindings: values.headerBindings, ...businessSystemConnection(values) }
      const saved = await (system ? updateBusinessSystem(system.id, input) : createBusinessSystem(input))
      if (system) void invalidateResource(resourceKeys.businessSystem(system.id))
      void invalidateResource(resourceKeys.businessSystems())
      void invalidateResource(resourceKeys.agentBusinessSystemOptions())
      return saved
    },
    savedValues: (saved, values) => ({ ...values, baseUrl: saved.http?.baseUrl ?? values.baseUrl }),
    onSubmitted: () => {
      toast.success(t("businessSystem.form.createSuccess"))
      navigate(listPath)
    },
    errorMessage: t("businessSystem.form.saveError"),
    errorFields,
    logLabel: "业务系统保存",
  })
  const disabled = testing || form.formState.isSubmitting
  const secretLabels = { show: t("businessSystem.form.showSecret"), hide: t("businessSystem.form.hideSecret") }

  return (
    <form className="w-full space-y-9" onSubmit={form.handleSubmit(submit)} noValidate>
      <FieldGroup>
        <FormInputField
          name="name"
          control={form.control}
          disabled={disabled}
          label={t("businessSystem.form.name")}
          autoFocus={!editing}
        />
        <Controller
          name="transport"
          control={form.control}
          render={({ field }) => (
            <Field>
              <FieldLabel htmlFor="business-system-transport" required>
                {t("businessSystem.form.transport")}
              </FieldLabel>
              <NativeSelect {...field} id="business-system-transport" disabled={disabled || editing}>
                {businessSystemTransports.map((value) => (
                  <option key={value} value={value}>
                    {t(`businessSystem.transports.${value}`)}
                  </option>
                ))}
              </NativeSelect>
            </Field>
          )}
        />
        {mcp ? (
          <>
            <FormInputField
              name="url"
              control={form.control}
              disabled={disabled}
              label={t("businessSystem.form.url")}
              inputMode="url"
            />
            <Controller
              name="serverType"
              control={form.control}
              render={({ field, fieldState }) => (
                <Field data-invalid={fieldState.invalid}>
                  <FieldLabel htmlFor="business-system-server-type" required>
                    {t("businessSystem.form.serverType")}
                  </FieldLabel>
                  <NativeSelect
                    {...field}
                    id="business-system-server-type"
                    disabled={disabled}
                    required
                    aria-invalid={fieldState.invalid}
                  >
                    {mcpServerTypes.map((type) => (
                      <option key={type} value={type}>
                        {type}
                      </option>
                    ))}
                  </NativeSelect>
                </Field>
              )}
            />
          </>
        ) : (
          <>
            <Controller
              name="specSource"
              control={form.control}
              render={({ field }) => (
                <Field>
                  <FieldLabel htmlFor="business-system-spec-source" required>
                    {t("businessSystem.form.specSource")}
                  </FieldLabel>
                  <NativeSelect {...field} id="business-system-spec-source" disabled={disabled}>
                    {specSources.map((source) => (
                      <option key={source} value={source}>
                        {t(`businessSystem.form.specSources.${source}`)}
                      </option>
                    ))}
                  </NativeSelect>
                </Field>
              )}
            />
            {specSource === "url" ? (
              <FormInputField
                name="specUrl"
                control={form.control}
                disabled={disabled}
                label={t("businessSystem.form.specUrl")}
                inputMode="url"
              />
            ) : (
              <Controller
                name="spec"
                control={form.control}
                render={({ field, fieldState }) => (
                  <Field data-invalid={fieldState.invalid}>
                    <FieldLabel htmlFor="business-system-spec" required>
                      {t("businessSystem.form.spec")}
                    </FieldLabel>
                    <Textarea
                      {...field}
                      id="business-system-spec"
                      rows={10}
                      spellCheck={false}
                      className="max-h-96 font-mono text-xs"
                      disabled={disabled}
                      required
                      aria-invalid={fieldState.invalid}
                    />
                    <FieldDescription>{t("businessSystem.form.specHelp")}</FieldDescription>
                  </Field>
                )}
              />
            )}
            <Controller
              name="baseUrl"
              control={form.control}
              render={({ field, fieldState }) => (
                <Field data-invalid={fieldState.invalid}>
                  <FieldLabel htmlFor="business-system-base-url">{t("businessSystem.form.baseUrl")}</FieldLabel>
                  <Input {...field} id="business-system-base-url" inputMode="url" disabled={disabled} aria-invalid={fieldState.invalid} />
                  <FieldDescription>{t("businessSystem.form.baseUrlHelp")}</FieldDescription>
                </Field>
              )}
            />
          </>
        )}
        <Controller
          name="credentialKind"
          control={form.control}
          render={({ field }) => (
            <Field>
              <FieldLabel htmlFor="business-system-credential-kind" required>
                {t("businessSystem.form.credentialKind")}
              </FieldLabel>
              <NativeSelect {...field} id="business-system-credential-kind" disabled={disabled}>
                {businessSystemCredentialKinds.map((kind) => (
                  <option key={kind} value={kind}>
                    {t(`businessSystem.credentialKinds.${kind}`)}
                  </option>
                ))}
              </NativeSelect>
            </Field>
          )}
        />
        {credentialKind === BusinessSystemCredentialKind.BusinessSystemCredentialHeader ? (
          <FormInputField
            name="credentialHeaderName"
            control={form.control}
            disabled={disabled}
            label={t("businessSystem.form.credentialHeaderName")}
            className="font-mono"
          />
        ) : null}
        {credentialKind === BusinessSystemCredentialKind.BusinessSystemCredentialBearer ||
        credentialKind === BusinessSystemCredentialKind.BusinessSystemCredentialHeader ? (
          <FormInputField
            name="credentialToken"
            control={form.control}
            disabled={disabled}
            label={t(credentialKind === BusinessSystemCredentialKind.BusinessSystemCredentialBearer ? "businessSystem.form.token" : "businessSystem.form.headerSecret")}
            autoComplete="new-password"
            passwordVisibilityLabels={secretLabels}
          />
        ) : null}
        {credentialKind === BusinessSystemCredentialKind.BusinessSystemCredentialBasic ? (
          <>
            <FormInputField
              name="credentialUsername"
              control={form.control}
              disabled={disabled}
              label={t("businessSystem.form.username")}
              autoComplete="off"
            />
            <FormInputField
              name="credentialPassword"
              control={form.control}
              disabled={disabled}
              label={t("businessSystem.form.password")}
              required={false}
              autoComplete="new-password"
              passwordVisibilityLabels={secretLabels}
            />
          </>
        ) : null}
        <HeaderBindingsField form={form} disabled={disabled} />
      </FieldGroup>
      <FormActions saving={form.formState.isSubmitting} disabled={testing} cancelTo={listPath} submit={!editing}>
        <Button type="button" variant="outline" disabled={disabled} onClick={() => void testConnection()}>
          {testing ? t("connection.testing") : t("connection.test")}
        </Button>
      </FormActions>
    </form>
  )
}

/** 逐行编辑请求头名称与填入的可信上下文值，增删行后重新校验全部请求头名称。 */
function HeaderBindingsField({
  form,
  disabled,
}: {
  form: UseFormReturn<BusinessSystemFormValues>
  disabled: boolean
}) {
  const { t } = useTranslation("integrations")
  const id = useId()
  const { fields, append, remove } = useFieldArray({ control: form.control, name: "headerBindings", keyName: "fieldKey" })
  const headerNames = fields.map((_, index) => `headerBindings.${index}.header` as const)
  // 行数变化并渲染后重新校验全部请求头名称：新增的空行出现提示，删除后清除已失效的重复提示。
  const latestNames = useRef(headerNames)
  latestNames.current = headerNames
  const rowCount = useRef(fields.length)
  useEffect(() => {
    if (rowCount.current === fields.length) return
    rowCount.current = fields.length
    void form.trigger([...latestNames.current])
  }, [form, fields.length])

  return (
    <div className="space-y-3" role="group" aria-labelledby={`${id}-label`}>
      <div>
        <div id={`${id}-label`} className="text-sm font-medium">
          {t("businessSystem.form.headerBindings")}
        </div>
        <FieldDescription className="mt-1">{t("businessSystem.form.headerBindingsHelp")}</FieldDescription>
      </div>
      {fields.length > 0 ? (
        <div className="divide-y rounded-lg border">
          {fields.map((item, index) => (
            <div className="flex items-center gap-3 px-4 py-3" key={item.fieldKey}>
              <Controller
                name={`headerBindings.${index}.header`}
                control={form.control}
                rules={{ deps: headerNames }}
                render={({ field, fieldState }) => (
                  <Input
                    {...field}
                    // 已有提示的请求头名称在输入时重新校验全部行，新建页提交前同样随输入清除提示。
                    onChange={(event) => {
                      field.onChange(event)
                      if (fieldState.invalid) void form.trigger(headerNames)
                    }}
                    className="min-w-0 flex-1 font-mono"
                    disabled={disabled}
                    aria-label={t("businessSystem.form.headerName")}
                    aria-invalid={fieldState.invalid}
                    required
                  />
                )}
              />
              <Controller
                name={`headerBindings.${index}.value`}
                control={form.control}
                render={({ field }) => (
                  <NativeSelect
                    {...field}
                    className="w-44 shrink-0"
                    disabled={disabled}
                    aria-label={t("businessSystem.form.headerValue")}
                  >
                    {contextValues.map((value) => (
                      <option key={value} value={value}>
                        {t(`businessSystem.contextValues.${value}`)}
                      </option>
                    ))}
                  </NativeSelect>
                )}
              />
              <Button
                type="button"
                variant="ghost"
                size="icon"
                disabled={disabled}
                aria-label={t("businessSystem.form.removeHeader")}
                title={t("businessSystem.form.removeHeader")}
                onClick={() => remove(index)}
              >
                <XIcon />
              </Button>
            </div>
          ))}
        </div>
      ) : null}
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={disabled}
        onClick={() => append({ header: "", value: ContextValue.CustomerUserID })}
      >
        {t("businessSystem.form.addHeader")}
      </Button>
    </div>
  )
}

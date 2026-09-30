/** MCP 服务新增与编辑页。 */
import { useEffect, useMemo, useRef, useState } from "react"
import { Controller, useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate, useParams } from "react-router"
import { toast } from "sonner"

import {
  MCPServerType,
  createMCPServer,
  getMCPServer,
  isApiError,
  updateMCPServer,
  testMCPServerConnection,
} from "@/api"
import { FormActions } from "@/components/form/form-actions"
import { FormInputField } from "@/components/form/form-input-field"
import { SwitchCardField } from "@/components/form/switch-card-field"
import { ResourceContent } from "@/components/resource-content"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect } from "@/components/ui/native-select"
import {
  createMCPServerSchema,
  type MCPServerFormValues,
} from "@/features/integrations/mcp-servers/mcp-server-schema"
import { MCPServerToolPurposes } from "@/features/integrations/mcp-servers/mcp-server-tool-purposes"
import { resourceKeys } from "@/hooks/resource-keys"
import { useFormSave } from "@/hooks/use-form-save"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

const listPath = "/tools"

/** 编辑 MCP 服务名称、地址、服务器类型、认证令牌、按客户查询开关与工具用途。 */
export function MCPServerFormPage({ mode }: { mode: "create" | "edit" }) {
  const { t } = useTranslation(["integrations", "common"])
  const navigate = useNavigate()
  const { mcpServerId = "" } = useParams()
  const invalidateResource = useResourceInvalidator()
  const [testing, setTesting] = useState(false)
  const schema = useMemo(
    () =>
      createMCPServerSchema({
        nameRequired: t("mcpServer.validation.nameRequired"),
        nameTooLong: t("mcpServer.validation.nameTooLong"),
        serverTypeInvalid: t("mcpServer.validation.serverTypeInvalid"),
        urlRequired: t("mcpServer.validation.urlRequired"),
        urlTooLong: t("mcpServer.validation.urlTooLong"),
        urlInvalid: t("mcpServer.validation.urlInvalid"),
      }),
    [t],
  )
  const form = useForm<MCPServerFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    // 编辑时离开字段即校验以便自动保存；新建时等提交再校验，避免原生校验把焦点锁在必填项上。
    mode: mode === "edit" ? "onBlur" : "onSubmit",
    defaultValues: {
      name: "",
      url: "",
      serverType: MCPServerType.MCPServerTypeStreamableHTTP,
      authorizationToken: "",
      customerScoped: false,
    },
  })
  const detail = useResource(resourceKeys.mcpServer(mcpServerId), () => getMCPServer(mcpServerId), {
    enabled: mode === "edit",
    // 工具目录更新期间轮询详情。
    refetchInterval: (data) => data?.toolsUpdating ? 1000 : false,
  })
  const mcpServer = detail.data
  const filledServerID = useRef("")

  /** 详情首次就绪后回填 MCP 服务表单，之后的刷新只更新工具目录，保留表单中的编辑。 */
  useEffect(() => {
    if (!mcpServer || filledServerID.current === mcpServer.id) return
    filledServerID.current = mcpServer.id
    form.reset({
      name: mcpServer.name,
      url: mcpServer.url,
      serverType: mcpServer.serverType,
      authorizationToken: mcpServer.authorizationToken,
      customerScoped: mcpServer.customerScoped,
    })
  }, [mcpServer, form])

  /** 测试当前未保存的连接配置。 */
  async function testConnection() {
    if (testing || form.formState.isSubmitting) return
    if (!(await form.trigger(["url", "serverType", "authorizationToken"], { shouldFocus: true }))) return
    setTesting(true)
    try {
      const { url, serverType, authorizationToken } = form.getValues()
      await testMCPServerConnection({ url, serverType, authorizationToken })
      if (mounted.current) toast.success(t("mcpServer.connection.success"))
    } catch (error) {
      if (!mounted.current || recoverSession(error, navigate)) return
      toast.error(isApiError(error) ? apiErrorMessage(error) : t("mcpServer.connection.error"))
    } finally {
      if (mounted.current) setTesting(false)
    }
  }

  /** 创建或保存 MCP 服务，并由服务端提交工具更新任务。 */
  // 编辑已有服务时边改边存，新建仍由底部按钮提交并跳回列表。
  const { submit, mounted } = useFormSave({
    form,
    schema,
    autoSave: mode === "edit",
    save: async (values) => {
      await (mode === "create"
        ? createMCPServer(values)
        : updateMCPServer(mcpServerId, values))
      // 编辑时服务改为按客户查询会从助理配置中移除该服务，助理详情一并失效。
      if (mode === "edit") {
        void invalidateResource(resourceKeys.mcpServer(mcpServerId))
        void invalidateResource(resourceKeys.assistant())
      }
      void invalidateResource(resourceKeys.mcpServers())
      void invalidateResource(resourceKeys.agentMCPServerOptions())
    },
    onSubmitted: () => {
      toast.success(t("mcpServer.form.createSuccess"))
      navigate(listPath)
    },
    errorMessage: t("mcpServer.form.saveError"),
    errorFields: ["name", "url", "serverType", "authorizationToken"],
    logLabel: "MCP 服务保存",
  })

  const title =
    mode === "create" ? t("mcpServer.form.createTitle") : t("mcpServer.form.editTitle")

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
      <PageHeader
        title={title}
        description={t(
          mode === "create"
            ? "mcpServer.form.createDescription"
            : "mcpServer.form.editDescription",
        )}
        backTo={mode === "edit" ? listPath : undefined}
      />
      <PageContent variant="form">
        <ResourceContent
          resources={mode === "edit" ? detail : []}
          errorMessage={t("mcpServer.form.loadError")}
        >
          <form
            className="w-full space-y-9"
            onSubmit={form.handleSubmit(submit)}
            noValidate
          >
            <FieldGroup>
              <FormInputField
                name="name"
                control={form.control}
                disabled={testing || form.formState.isSubmitting}
                label={t("mcpServer.form.name")}
                autoFocus={mode === "create"}
              />
              <FormInputField
                name="url"
                control={form.control}
                disabled={testing || form.formState.isSubmitting}
                label={t("mcpServer.form.url")}
                inputMode="url"
              />
              <Controller
                name="serverType"
                control={form.control}
                render={({ field, fieldState }) => (
                  <Field data-invalid={fieldState.invalid}>
                    <FieldLabel htmlFor="serverType" required>
                      {t("mcpServer.form.serverType")}
                    </FieldLabel>
                    <NativeSelect
                      {...field}
                      id="serverType"
                      disabled={testing || form.formState.isSubmitting}
                      required
                      aria-invalid={fieldState.invalid}
                    >
                      {[
                        MCPServerType.MCPServerTypeSSE,
                        MCPServerType.MCPServerTypeStreamableHTTP,
                      ].map((type) => (
                        <option key={type} value={type}>
                          {type}
                        </option>
                      ))}
                    </NativeSelect>
                  </Field>
                )}
              />
              <FormInputField
                name="authorizationToken"
                control={form.control}
                disabled={testing || form.formState.isSubmitting}
                label={t("mcpServer.form.authorizationToken")}
                required={false}
                autoComplete="new-password"
                passwordVisibilityLabels={{
                  show: t("mcpServer.form.showToken"),
                  hide: t("mcpServer.form.hideToken"),
                }}
              />
              <Controller
                name="customerScoped"
                control={form.control}
                render={({ field }) => (
                  <SwitchCardField
                    id="mcp-server-customer-scoped"
                    name={field.name}
                    label={t("mcpServer.form.customerScoped")}
                    description={t("mcpServer.form.customerScopedHelp")}
                    checked={field.value}
                    disabled={testing || form.formState.isSubmitting}
                    onBlur={field.onBlur}
                    onCheckedChange={field.onChange}
                    ref={field.ref}
                  />
                )}
              />
              {mode === "edit" && mcpServer ? <MCPServerToolPurposes server={mcpServer} /> : null}
            </FieldGroup>
            <FormActions
              saving={form.formState.isSubmitting}
              disabled={testing}
              cancelTo={listPath}
              submit={mode === "create"}
            >
              <Button
                type="button"
                variant="outline"
                disabled={testing || form.formState.isSubmitting}
                onClick={() => void testConnection()}
              >
                {testing ? t("connection.testing") : t("connection.test")}
              </Button>
            </FormActions>
          </form>
        </ResourceContent>
      </PageContent>
    </div>
  )
}

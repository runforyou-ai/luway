/** 首次安装表单。 */
import { useMemo } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"

import { isApiError, install, SessionState } from "@/api"
import { FormInputField } from "@/components/form/form-input-field"
import { Button } from "@/components/ui/button"
import { FieldGroup } from "@/components/ui/field"
import { useStartup } from "@/contexts/startup-context"
import { createSetupSchema, type SetupFormValues } from "@/features/installation/setup-schema"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { enterWorkspace } from "@/lib/workspace-route"
import { zodResolver } from "@/lib/zod-resolver"

/** 创建第一个工作区和平台管理员账号。 */
export function SetupForm() {
  const { t } = useTranslation("setup")
  const navigate = useNavigate()
  const reportError = useRequestErrorReporter()
  const { completeStartup } = useStartup()
  const schema = useMemo(() => createSetupSchema(t), [t])
  const form = useForm<SetupFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: {
      publicURL: window.location.origin,
      workspaceName: "",
      displayName: "",
      email: "",
      password: "",
    },
  })

  /** 提交首次安装并进入新建的工作区。 */
  async function submitSetup(values: SetupFormValues) {
    try {
      const workspace = await install(values)
      completeStartup()
      enterWorkspace(workspace.slug, "/inbox", { replace: true })
    } catch (error) {
      // 平台已由他人完成安装时回到登录页。
      if (isApiError(error) && error.state === SessionState.Login) {
        completeStartup()
        navigate("/login", { replace: true })
        return
      }
      reportError(error, { fields: ["publicURL", "workspaceName", "displayName", "email", "password"] })
    }
  }

  const { isSubmitting } = form.formState

  return (
    <form className="space-y-9" onSubmit={form.handleSubmit(submitSetup)} noValidate>
      <FieldGroup>
        <FormInputField
          name="publicURL"
          control={form.control}
          label={t("publicURLLabel")}
          description={t("publicURLHelp")}
          type="url"
        />
        <FormInputField
          name="workspaceName"
          control={form.control}
          label={t("workspaceNameLabel")}
          autoFocus
        />
        <FormInputField
          name="displayName"
          control={form.control}
          label={t("displayNameLabel")}
          autoComplete="name"
        />
        <FormInputField
          name="email"
          control={form.control}
          label={t("emailLabel")}
          type="email"
          autoComplete="email"
        />
        <FormInputField
          name="password"
          control={form.control}
          label={t("passwordLabel")}
          type="password"
          autoComplete="new-password"
          passwordVisibilityLabels={{
            show: t("showPassword"),
            hide: t("hidePassword"),
          }}
        />
      </FieldGroup>
      <Button type="submit" className="w-full" disabled={isSubmitting}>
        {isSubmitting ? <LoaderCircleIcon className="animate-spin" /> : null}
        {isSubmitting ? t("submitting") : t("submit")}
      </Button>
    </form>
  )
}

/** 首次安装表单。 */
import { useEffect, useMemo, useState } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { isApiError, install, SessionState } from "@/api"
import { recoverSession } from "@/lib/session-navigation"
import { FormInputField } from "@/components/form/form-input-field"
import { Button } from "@/components/ui/button"
import { FieldGroup } from "@/components/ui/field"
import { randomWorkspaceSlug, suggestWorkspaceSlug } from "@/lib/workspace-schema"
import {
  createSetupSchema,
  type SetupFormValues,
} from "@/features/installation/setup-schema"
import { useStartup } from "@/contexts/startup-context"
import { requestErrorMessage } from "@/lib/form-errors"
import { enterWorkspace } from "@/lib/workspace-route"
import { zodResolver } from "@/lib/zod-resolver"

/** 创建第一个工作区和平台管理员账号。 */
export function SetupForm() {
  const { t } = useTranslation("setup")
  const navigate = useNavigate()
  const { completeStartup } = useStartup()
  const schema = useMemo(() => createSetupSchema(t), [t])
  const form = useForm<SetupFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: {
      workspaceName: "",
      workspaceSlug: "",
      displayName: "",
      email: "",
      password: "",
    },
  })

  // 工作区标识未手动修改时按名称自动建议，名称无法转成标识时使用本页生成的随机标识。
  const [fallbackSlug] = useState(randomWorkspaceSlug)
  const { getFieldState, setValue, watch } = form
  const workspaceName = watch("workspaceName")
  useEffect(() => {
    if (!getFieldState("workspaceSlug").isDirty) {
      setValue("workspaceSlug", suggestWorkspaceSlug(workspaceName, fallbackSlug))
    }
  }, [fallbackSlug, getFieldState, setValue, workspaceName])

  /** 提交首次安装并进入新建的工作区。 */
  async function submitSetup(values: SetupFormValues) {
    try {
      await install(values)
      completeStartup()
      enterWorkspace(values.workspaceSlug, "/inbox", { replace: true })
    } catch (error) {
      // 平台已由他人完成安装时回到登录页。
      if (isApiError(error) && error.state === SessionState.SessionStateLogin) {
        completeStartup()
        navigate("/login", { replace: true })
        return
      }
      if (recoverSession(error, navigate)) {
        return
      }
      toast.error(
        requestErrorMessage(error, [
          "workspaceName",
          "workspaceSlug",
          "displayName",
          "email",
          "password",
        ]),
      )
    }
  }

  const { isSubmitting } = form.formState

  return (
    <form className="space-y-9" onSubmit={form.handleSubmit(submitSetup)} noValidate>
      <FieldGroup>
        <FormInputField
          name="workspaceName"
          control={form.control}
          label={t("workspaceNameLabel")}
          autoFocus
        />
        <FormInputField
          name="workspaceSlug"
          control={form.control}
          label={t("workspaceSlugLabel")}
          autoCapitalize="none"
          autoCorrect="off"
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

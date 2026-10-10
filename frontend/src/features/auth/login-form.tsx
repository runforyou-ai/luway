/** 登录表单。 */
import { LoaderCircleIcon } from "lucide-react"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"

import { login } from "@/api"
import { FormInputField } from "@/components/form/form-input-field"
import { Button } from "@/components/ui/button"
import { FieldGroup } from "@/components/ui/field"
import {
  loginSchema,
  type LoginFormValues,
} from "@/features/auth/login-schema"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { zodResolver } from "@/lib/zod-resolver"

/** 校验并提交登录。 */
export function LoginForm() {
  const { t } = useTranslation("auth")
  const navigate = useNavigate()
  const reportError = useRequestErrorReporter()

  const form = useForm<LoginFormValues>({
    resolver: zodResolver(loginSchema),
    shouldUseNativeValidation: true,
    defaultValues: {
      email: "",
      password: "",
    },
  })

  /** 提交登录并前往工作区入口。 */
  async function submitLogin(values: LoginFormValues) {
    try {
      await login(values)
      navigate("/", { replace: true })
    } catch (error) {
      reportError(error, { fields: ["email", "password"] })
    }
  }

  const { isSubmitting } = form.formState

  return (
    <form className="space-y-9" onSubmit={form.handleSubmit(submitLogin)} noValidate>
      <FieldGroup>
        <FormInputField
          name="email"
          control={form.control}
          label={t("emailLabel")}
          type="email"
          autoComplete="email"
          autoFocus
        />
        <FormInputField
          name="password"
          control={form.control}
          label={t("passwordLabel")}
          type="password"
          autoComplete="current-password"
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

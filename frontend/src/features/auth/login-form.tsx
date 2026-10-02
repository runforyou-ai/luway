/** 登录表单。 */
import { useMemo } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { login } from "@/api"
import { recoverSession } from "@/lib/session-navigation"
import { FormInputField } from "@/components/form/form-input-field"
import { Button } from "@/components/ui/button"
import { FieldGroup } from "@/components/ui/field"
import {
  createLoginSchema,
  type LoginFormValues,
} from "@/features/auth/login-schema"
import { requestErrorMessage } from "@/lib/form-errors"
import { zodResolver } from "@/lib/zod-resolver"

/** 校验并提交登录。 */
export function LoginForm() {
  const { t } = useTranslation("auth")
  const navigate = useNavigate()
  const schema = useMemo(() => createLoginSchema(t), [t])
  const form = useForm<LoginFormValues>({
    resolver: zodResolver(schema),
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
      if (recoverSession(error, navigate)) {
        return
      }
      toast.error(requestErrorMessage(error, ["email", "password"]))
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

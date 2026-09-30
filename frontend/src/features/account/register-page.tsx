/** 注册页，部署开放注册或持有邀请链接时可用。 */
import { useEffect, useMemo } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { Link, Navigate, useNavigate, useSearchParams } from "react-router"
import { toast } from "sonner"

import { loadInstallationStatus, register } from "@/api"
import { FormInputField } from "@/components/form/form-input-field"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { FieldGroup } from "@/components/ui/field"
import { createRegisterSchema, type RegisterFormValues } from "@/features/account/register-schema"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { requestErrorMessage } from "@/lib/form-errors"
import { invitationPath, rememberPendingInvitation } from "@/lib/pending-invitation"
import { recoverSession } from "@/lib/session-navigation"
import { zodResolver } from "@/lib/zod-resolver"

/** 校验并提交注册，成功后进入工作区入口；通过邀请注册时回到邀请页。 */
export function RegisterPage() {
  const { t } = useTranslation("auth")
  const navigate = useNavigate()
  const invitation = useSearchParams()[0].get("invitation") ?? ""
  // 从注册页转去登录时，登录后同样回到邀请页。
  useEffect(() => {
    if (invitation) rememberPendingInvitation(invitation)
  }, [invitation])
  const installation = useResource(resourceKeys.installationStatus(), (signal) => loadInstallationStatus(signal), { staleTime: 0 })
  const schema = useMemo(() => createRegisterSchema(t), [t])
  const form = useForm<RegisterFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { displayName: "", email: "", password: "" },
  })

  if (installation.data && !installation.data.registrationOpen && !invitation) return <Navigate to="/login" replace />

  /** 提交注册并前往工作区入口。 */
  async function submitRegister(values: RegisterFormValues) {
    try {
      await register({ ...values, invitationToken: invitation })
      navigate(invitation ? invitationPath(invitation) : "/", { replace: true })
    } catch (error) {
      if (recoverSession(error, navigate)) return
      toast.error(requestErrorMessage(error, ["displayName", "email", "password"]))
    }
  }

  const { isSubmitting } = form.formState

  return (
    <main className="flex min-h-dvh w-full items-center justify-center px-6 pt-[max(1.5rem,env(safe-area-inset-top))] pb-[max(1.5rem,env(safe-area-inset-bottom))] md:p-10">
      <div className="w-full max-w-sm space-y-6">
        <Card>
          <CardHeader>
            <CardTitle>{t("registerTitle")}</CardTitle>
            <CardDescription>{invitation ? t("registerInvitationDescription") : t("registerDescription")}</CardDescription>
          </CardHeader>
          <CardContent>
            <form className="space-y-9" onSubmit={form.handleSubmit(submitRegister)} noValidate>
              <FieldGroup>
                <FormInputField name="displayName" control={form.control} label={t("displayNameLabel")} autoComplete="name" autoFocus />
                <FormInputField name="email" control={form.control} label={t("emailLabel")} type="email" autoComplete="email" />
                <FormInputField
                  name="password"
                  control={form.control}
                  label={t("newPasswordLabel")}
                  type="password"
                  autoComplete="new-password"
                  passwordVisibilityLabels={{ show: t("showPassword"), hide: t("hidePassword") }}
                />
              </FieldGroup>
              <Button type="submit" className="w-full" disabled={isSubmitting}>
                {isSubmitting ? <LoaderCircleIcon className="animate-spin" /> : null}
                {isSubmitting ? t("registerSubmitting") : t("registerSubmit")}
              </Button>
            </form>
          </CardContent>
        </Card>
        <p className="text-center text-sm text-muted-foreground">
          {t("loginPrompt")}{" "}
          <Link to="/login" replace className="font-medium text-foreground underline-offset-4 hover:underline">
            {t("loginLink")}
          </Link>
        </p>
      </div>
    </main>
  )
}

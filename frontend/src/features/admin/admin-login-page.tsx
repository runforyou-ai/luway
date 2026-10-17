/** 平台后台登录页。 */
import { useMemo } from "react"
import { LoaderCircleIcon, ShieldCheckIcon } from "lucide-react"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { Navigate, useNavigate } from "react-router"
import { z } from "zod"

import { adminLogin, loadAdminAccount } from "@/api"
import { FormInputField } from "@/components/form/form-input-field"
import { PageLoading } from "@/components/page-loading"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { FieldGroup } from "@/components/ui/field"
import { adminOverviewPath } from "@/features/admin/admin-paths"
import { AdminSurface } from "@/lib/admin-surface"
import { resourceKeys } from "@/hooks/resource-keys"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResource } from "@/hooks/use-resource"
import { useBrandName } from "@/lib/brand"
import { zodResolver } from "@/lib/zod-resolver"

/** 已登录后台时进入概览，否则展示后台登录表单。 */
export function AdminLoginPage() {
  const { t } = useTranslation("admin")
  const productName = useBrandName()
  const navigate = useNavigate()
  const reportError = useRequestErrorReporter()
  const session = useResource(resourceKeys.adminAccount(), (signal) => loadAdminAccount(signal), {
    staleTime: 0,
    recoverSession: false,
  })
  const schema = useMemo(
    () =>
      z.object({
        email: z.string().trim().min(1).email(),
        password: z.string().min(1),
      }),
    [],
  )
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { email: "", password: "" },
  })

  // 仅在无读取错误时用缓存判定已登录，避免退出后旧缓存把页面弹回后台。
  if (session.data?.isPlatformAdmin && !session.error) {
    return <Navigate to={adminOverviewPath} replace />
  }
  if (!session.error && !session.data) {
    return (
      <AdminSurface>
        <PageLoading />
      </AdminSurface>
    )
  }

  async function submit(values: z.infer<typeof schema>) {
    try {
      await adminLogin(values)
      void session.refresh()
      navigate(adminOverviewPath, { replace: true })
    } catch (error) {
      reportError(error, { fields: ["email", "password"] })
    }
  }

  const { isSubmitting } = form.formState

  return (
    <AdminSurface>
      <div data-admin className="flex min-h-svh w-full items-center justify-center bg-background px-4 py-10 text-foreground">
        <Card className="w-full max-w-sm gap-6 py-8 shadow-sm">
          <CardHeader className="gap-3 px-8">
            <span className="flex size-10 items-center justify-center rounded-lg bg-sidebar text-sidebar-primary">
              <ShieldCheckIcon className="size-5" aria-hidden="true" />
            </span>
            <CardTitle className="text-xl font-semibold tracking-tight">{t("login.title")}</CardTitle>
            <CardDescription>{t("login.description", { product: productName })}</CardDescription>
          </CardHeader>
          <CardContent className="px-8">
            <form className="space-y-9" onSubmit={form.handleSubmit(submit)} noValidate>
              <FieldGroup>
                <FormInputField name="email" control={form.control} label={t("login.emailLabel")} type="email" autoComplete="username" autoFocus />
                <FormInputField
                  name="password"
                  control={form.control}
                  label={t("login.passwordLabel")}
                  type="password"
                  autoComplete="current-password"
                  passwordVisibilityLabels={{ show: t("login.showPassword"), hide: t("login.hidePassword") }}
                />
              </FieldGroup>
              <Button type="submit" className="w-full" disabled={isSubmitting}>
                {isSubmitting ? <LoaderCircleIcon className="animate-spin" /> : null}
                {isSubmitting ? t("login.submitting") : t("login.submit")}
              </Button>
            </form>
          </CardContent>
        </Card>
      </div>
    </AdminSurface>
  )
}

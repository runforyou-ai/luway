/** 官方账号授权回调页。 */
import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { useNavigate, useSearchParams } from "react-router"

import { completeOfficialLogin, OfficialLoginStateError } from "@/api"
import { LoadingIndicator } from "@/components/loading-indicator"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { useBrandName } from "@/lib/brand"
import { requestErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

/** 用授权回调参数完成官方账号登录后前往工作区入口，失败时提示并提供重新登录。 */
export function OfficialLoginCallbackPage() {
  const { t } = useTranslation("auth")
  const navigate = useNavigate()
  const productName = useBrandName()
  const [searchParams] = useSearchParams()
  const [failure, setFailure] = useState<string | null>(null)
  const started = useRef(false)
  const active = useRef(true)

  useEffect(() => {
    active.current = true
    // 授权码只能交换一次，同一页面实例只提交一次；离开页面后忽略交换结果。
    if (!started.current) {
      started.current = true
      const code = searchParams.get("code")
      const state = searchParams.get("state")
      if (searchParams.get("error") || !code || !state) {
        setFailure(t("officialDenied"))
      } else {
        completeOfficialLogin(state, code, () => active.current)
          .then((account) => {
            if (account && active.current) navigate("/", { replace: true })
          })
          .catch((error: unknown) => {
            if (!active.current) return
            if (error instanceof OfficialLoginStateError) {
              setFailure(t("officialExpired"))
              return
            }
            if (recoverSession(error, navigate)) {
              return
            }
            setFailure(requestErrorMessage(error))
          })
      }
    }
    return () => {
      active.current = false
    }
  }, [navigate, searchParams, t])

  return (
    <main className="flex min-h-dvh w-full items-center justify-center px-6 pt-[max(1.5rem,env(safe-area-inset-top))] pb-[max(1.5rem,env(safe-area-inset-bottom))] md:p-10">
      <div className="w-full max-w-sm">
        <p className="mb-8 text-center text-xl font-medium tracking-tight">{productName}</p>
        {failure ? (
          <Card>
            <CardHeader>
              <CardTitle>{t("officialFailedTitle")}</CardTitle>
              <CardDescription>{failure}</CardDescription>
            </CardHeader>
            <CardContent>
              <Button type="button" className="w-full" onClick={() => navigate("/login", { replace: true })}>
                {t("signInAgain")}
              </Button>
            </CardContent>
          </Card>
        ) : (
          <div className="flex justify-center">
            <LoadingIndicator>
              <span className="text-sm text-muted-foreground">{t("submitting")}</span>
            </LoadingIndicator>
          </div>
        )}
      </div>
    </main>
  )
}

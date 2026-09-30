/** 托管企业的官方账号登录入口，以及原生端暂不支持官方账号登录的说明。 */
import { useState } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { startOfficialLogin } from "@/api"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { requestErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

/** 发起官方账号登录并跳转到官方账号授权页。 */
export function OfficialLoginCard() {
  const { t } = useTranslation("auth")
  const navigate = useNavigate()
  const [redirecting, setRedirecting] = useState(false)

  /** 登记登录尝试后离开当前页面前往授权页。 */
  async function beginOfficialLogin() {
    setRedirecting(true)
    try {
      window.location.assign(await startOfficialLogin())
    } catch (error) {
      setRedirecting(false)
      if (recoverSession(error, navigate)) {
        return
      }
      toast.error(requestErrorMessage(error))
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("title")}</CardTitle>
        <CardDescription>{t("officialDescription")}</CardDescription>
      </CardHeader>
      <CardContent>
        <Button type="button" className="w-full" disabled={redirecting} onClick={() => void beginOfficialLogin()}>
          {redirecting ? <LoaderCircleIcon className="animate-spin" /> : null}
          {redirecting ? t("officialRedirecting") : t("officialSubmit")}
        </Button>
      </CardContent>
    </Card>
  )
}

/** 说明当前客户端暂不能用官方账号登录托管企业。 */
export function OfficialLoginUnsupported() {
  const { t } = useTranslation("auth")

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("title")}</CardTitle>
        <CardDescription>{t("officialUnsupportedOnClient")}</CardDescription>
      </CardHeader>
    </Card>
  )
}

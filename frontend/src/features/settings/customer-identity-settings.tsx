/** 客户身份验证设置：企业网站签发登录用户签名身份所用的密钥与签发示例。 */
import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  getCustomerIdentitySecret,
  isApiError,
  regenerateCustomerIdentitySecret,
} from "@/api"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { useCopyFeedback } from "@/hooks/use-copy-feedback"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { embedSDKNames, useBrand } from "@/lib/brand"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

/** 企业网站服务端签发签名身份的 Node 示例。 */
const signingSample = `import jwt from "jsonwebtoken"

const customerToken = jwt.sign(
  {
    sub: String(user.id),
    name: user.name,
    email: user.email,
    attributes: { Plan: user.plan, Company: user.company },
    tags: user.isVip ? ["VIP"] : [],
  },
  process.env.CUSTOMER_IDENTITY_SECRET,
  { algorithm: "HS256", expiresIn: "12h" },
)`

/** 返回企业网站页面按当前品牌对象名传入签名身份的示例。 */
function widgetSample(sdk: ReturnType<typeof embedSDKNames>) {
  return `<script>
  window.${sdk.settings} = { customerToken: "<customerToken>" }
</script>

${sdk.global}.login(customerToken)
${sdk.global}.logout()
${sdk.global}.on("identityExpired", refreshCustomerToken)`
}

/** 读取客户身份密钥，提供复制、生成与重新生成，并展示签发示例。 */
export function CustomerIdentitySettings() {
  const { t } = useTranslation(["settings", "common"])
  const navigate = useNavigate()
  const sdk = embedSDKNames(useBrand())
  const secret = useResource(resourceKeys.customerIdentitySecret(), () =>
    getCustomerIdentitySecret(),
  )
  const { copied, copy } = useCopyFeedback<"secret">()
  const [confirming, setConfirming] = useState(false)
  const [pending, setPending] = useState(false)

  /** 复制密钥，失败时提示手动复制。 */
  async function copySecret(value: string) {
    if (!(await copy(value, "secret"))) toast.error(t("customerService.identity.copyError"))
  }

  /** 生成或重新生成密钥，成功后刷新显示。 */
  async function regenerate() {
    setPending(true)
    try {
      await regenerateCustomerIdentitySecret()
      await secret.refresh()
      setConfirming(false)
      toast.success(t("customerService.identity.generated"))
    } catch (error) {
      if (recoverSession(error, navigate)) return
      console.warn("生成客户身份密钥失败", error)
      toast.error(
        isApiError(error)
          ? apiErrorMessage(error)
          : t("customerService.identity.generateError"),
      )
    } finally {
      setPending(false)
    }
  }

  const value = secret.data?.secret ?? ""

  return (
    <ResourceContent
      resources={secret}
      errorMessage={t("customerService.identity.loadError")}
    >
      {secret.data ? (
        <FieldGroup className="gap-8">
          <Field>
            <FieldLabel>{t("customerService.identity.secret")}</FieldLabel>
            <FieldDescription>
              {t("customerService.identity.secretHelp")}
            </FieldDescription>
            {value ? (
              <div className="space-y-3">
                <div className="flex items-center gap-2 rounded-md border bg-muted/30 px-3 py-2">
                  <code className="flex min-h-8 min-w-0 flex-1 items-center font-mono text-sm break-all">
                    {value}
                  </code>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="shrink-0"
                    onClick={() => void copySecret(value)}
                  >
                    {copied === "secret" ? t("common:actions.copied") : t("common:actions.copy")}
                  </Button>
                </div>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setConfirming(true)}
                >
                  {t("customerService.identity.regenerate")}
                </Button>
              </div>
            ) : (
              <div>
                <Button
                  type="button"
                  size="sm"
                  disabled={pending}
                  onClick={() => void regenerate()}
                >
                  {pending
                    ? t("customerService.identity.generating")
                    : t("customerService.identity.generate")}
                </Button>
              </div>
            )}
          </Field>
          <Field>
            <FieldLabel>{t("customerService.identity.signing")}</FieldLabel>
            <FieldDescription>
              {t("customerService.identity.signingHelp")}
            </FieldDescription>
            <pre className="overflow-x-auto rounded-md border bg-muted/30 p-3 font-mono text-xs leading-5">
              {signingSample}
            </pre>
          </Field>
          <Field>
            <FieldLabel>{t("customerService.identity.widget")}</FieldLabel>
            <FieldDescription>
              {t("customerService.identity.widgetHelp", { sdkSettings: sdk.settings, sdkGlobal: sdk.global })}
            </FieldDescription>
            <pre className="overflow-x-auto rounded-md border bg-muted/30 p-3 font-mono text-xs leading-5">
              {widgetSample(sdk)}
            </pre>
          </Field>
        </FieldGroup>
      ) : null}
      <ConfirmationDialog
        open={confirming}
        pending={pending}
        title={t("customerService.identity.regenerateTitle")}
        description={t("customerService.identity.regenerateDescription")}
        onOpenChange={setConfirming}
        onConfirm={() => void regenerate()}
      />
    </ResourceContent>
  )
}

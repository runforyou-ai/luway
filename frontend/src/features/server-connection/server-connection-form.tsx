/** 企业服务器地址表单。 */
import { useEffect, useEffectEvent, useMemo, useRef, useState } from "react"
import { LoaderCircleIcon, SearchIcon } from "lucide-react"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { type Brand, ConnectReason, connectServer, DeploymentMode, isApiError, probeServer } from "@/api"
import { FormInputField } from "@/components/form/form-input-field"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { FieldGroup } from "@/components/ui/field"
import {
  createServerConnectionSchema,
  type ServerConnectionFormValues,
} from "@/features/server-connection/server-connection-schema"
import { useStartup } from "@/contexts/startup-context"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { applyBrand } from "@/lib/brand"
import { clearPendingServerLink, registerServerLinkReceiver } from "@/lib/server-link-queue"
import { resolveServerURL } from "@/lib/server-url"
import { apiErrorMessage } from "@/lib/form-errors"
import { zodResolver } from "@/lib/zod-resolver"

type DetectedServer = {
  serverUrl: string
  host: string
  brand: Brand
}

/** 检测服务器后确认连接。 */
export function ServerConnectionForm() {
  const { t } = useTranslation(["connection", "common"])
  const navigate = useNavigate()
  const { completeStartup, connectReason } = useStartup()
  const [detected, setDetected] = useState<DetectedServer | null>(null)
  const [detecting, setDetecting] = useState(false)
  const [connecting, setConnecting] = useState(false)
  // 检测请求代次，只采用最近一次检测的结果。
  const detectGeneration = useRef(0)
  const schema = useMemo(() => createServerConnectionSchema(t), [t])
  const form = useForm<ServerConnectionFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { serverUrl: "" },
  })
  const { getValues, reset, watch } = form
  const serverUrl = watch("serverUrl").trim()

  useEffect(() => {
    if (detected && detected.serverUrl !== serverUrl) {
      setDetected(null)
    }
  }, [detected, serverUrl])

  const savedUrl = useResource(resourceKeys.serverURL(), () => resolveServerURL()).data
  // 输入框为空时填入已保存的服务器地址。
  useEffect(() => {
    if (!savedUrl || getValues("serverUrl").trim() !== "") return
    reset({ serverUrl: savedUrl })
  }, [savedUrl, getValues, reset])

  // 连接链接交来的部署地址填入输入框并立即检测，由用户确认连接。
  const receiveServerLink = useEffectEvent((linkedUrl: string) => {
    reset({ serverUrl: linkedUrl })
    void detectServer({ serverUrl: linkedUrl })
  })
  useEffect(() => registerServerLinkReceiver((linkedUrl) => receiveServerLink(linkedUrl)), [])

  /** 检测服务器并展示服务器地址，返回检测结果；较早发起的检测结果到达时忽略；服务器尚未完成首次安装时说明需先在浏览器中完成安装。 */
  async function detectServer(values: ServerConnectionFormValues) {
    const generation = ++detectGeneration.current
    setDetecting(true)
    try {
      const status = await probeServer(values.serverUrl)
      if (generation !== detectGeneration.current) return null
      const serverUrl = values.serverUrl.trim()
      const host = new URL(serverUrl).host
      if (!status.installed && status.deploymentMode !== DeploymentMode.DeploymentModeManaged) {
        setDetected(null)
        toast.error(t("serverNotInstalled", { host }))
        return null
      }
      const server = { serverUrl, host, brand: status.brand }
      setDetected(server)
      return server
    } catch (error) {
      if (generation !== detectGeneration.current) return null
      setDetected(null)
      if (isApiError(error)) {
        toast.error(apiErrorMessage(error, ["serverUrl"]))
        return null
      }
      toast.error(t("connectionError"))
      return null
    } finally {
      if (generation === detectGeneration.current) setDetecting(false)
    }
  }

  /** 重新检测已保存的服务器，可用时直接连接。 */
  async function retrySavedServer() {
    if (!savedUrl) return
    reset({ serverUrl: savedUrl })
    const server = await detectServer({ serverUrl: savedUrl })
    if (server) await connectDetectedServer(server)
  }

  // 已保存的服务器暂时连不上时，恢复联网后自动重新检测。
  const retryWhenOnline = useEffectEvent(() => void retrySavedServer())
  useEffect(() => {
    if (connectReason !== ConnectReason.ConnectReasonUnreachable) return
    const onOnline = () => retryWhenOnline()
    window.addEventListener("online", onOnline)
    return () => window.removeEventListener("online", onOnline)
  }, [connectReason])

  /** 保存已检测的服务器并前往登录。 */
  async function connectDetectedServer(server = detected) {
    if (!server) {
      return
    }
    setConnecting(true)
    try {
      await connectServer(server.serverUrl)
      clearPendingServerLink()
      applyBrand(server.brand)
      completeStartup()
      navigate("/login", { replace: true })
    } catch (error) {
      if (isApiError(error)) {
        toast.error(apiErrorMessage(error, ["serverUrl"]))
        return
      }
      toast.error(t("connectionError"))
    } finally {
      setConnecting(false)
    }
  }

  const busy = detecting || connecting

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("title")}</CardTitle>
        <CardDescription>{t("description")}</CardDescription>
      </CardHeader>
      <CardContent>
        {/* 已保存的服务器暂时连不上或尚未完成首次安装时说明原因，连不上时提供重试。 */}
        {savedUrl && connectReason ? (
          <div role="status" className="mb-4 flex items-start gap-3 text-sm text-warning">
            <p className="min-w-0 flex-1">
              {connectReason === ConnectReason.ConnectReasonUnreachable
                ? t("savedServerUnreachable", { host: new URL(savedUrl).host })
                : t("serverNotInstalled", { host: new URL(savedUrl).host })}
            </p>
            {connectReason === ConnectReason.ConnectReasonUnreachable ? (
              <Button type="button" size="sm" variant="outline" className="shrink-0" disabled={busy} onClick={() => void retrySavedServer()}>
                {detecting || connecting ? <LoaderCircleIcon className="animate-spin" /> : null}
                {t("common:actions.retry")}
              </Button>
            ) : null}
          </div>
        ) : null}
        <form
          onSubmit={form.handleSubmit((values) => {
            if (detected) {
              void connectDetectedServer()
              return
            }
            void detectServer(values)
          })}
          noValidate
        >
          <FieldGroup>
            <div>
              <FormInputField
                name="serverUrl"
                control={form.control}
                label={t("serverUrlLabel")}
                type="url"
                autoCapitalize="none"
                autoCorrect="off"
                autoFocus
                endAction={
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    className="absolute top-1/2 right-1 -translate-y-1/2 text-muted-foreground"
                    disabled={busy}
                    aria-label={detecting ? t("detecting") : t("detect")}
                    onClick={() => void form.handleSubmit(detectServer)()}
                  >
                    {detecting ? (
                      <LoaderCircleIcon className="animate-spin" />
                    ) : (
                      <SearchIcon />
                    )}
                  </Button>
                }
              />
              <div
                className={
                  detected
                    ? "grid grid-rows-[1fr] transition-[grid-template-rows] duration-300 ease-out"
                    : "grid grid-rows-[0fr] transition-[grid-template-rows] duration-200 ease-out"
                }
              >
                <div className="min-h-0 overflow-hidden">
                  {detected ? (
                    <div
                      className="mt-3 flex items-center justify-between gap-4 rounded-lg bg-muted/50 px-3.5 py-3 animate-in fade-in-0 slide-in-from-top-1 duration-300"
                      aria-live="polite"
                    >
                      <p
                        className="min-w-0 truncate text-[15px] leading-none font-medium tracking-[-0.02em]"
                        title={detected.host}
                      >
                        {detected.host}
                      </p>
                      <Button
                        type="button"
                        size="sm"
                        className="h-7 shrink-0 px-3"
                        disabled={busy}
                        onClick={() => void connectDetectedServer()}
                      >
                        {connecting ? (
                          <LoaderCircleIcon className="animate-spin" />
                        ) : null}
                        {connecting ? t("connecting") : t("connect")}
                      </Button>
                    </div>
                  ) : null}
                </div>
              </div>
            </div>
          </FieldGroup>
        </form>
      </CardContent>
    </Card>
  )
}

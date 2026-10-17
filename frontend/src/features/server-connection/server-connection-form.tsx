/** 企业服务器地址表单。 */
import { useEffect, useEffectEvent, useMemo, useRef, useState } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { useForm } from "react-hook-form"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { type Brand, connectServer, isApiError, probeServer, SessionState } from "@/api"
import { serverURL } from "@/api/client"
import { Button } from "@/components/ui/button"
import { FieldGroup } from "@/components/ui/field"
import { useStartup } from "@/contexts/startup-context"
import { ServerAddressField } from "@/features/server-connection/server-address-field"
import {
  createServerConnectionSchema,
  serverConnectionURL,
  splitServerURL,
  type ServerConnectionFormValues,
} from "@/features/server-connection/server-connection-schema"
import { applyBrand } from "@/lib/brand"
import { apiErrorMessage } from "@/lib/form-errors"
import { clearPendingServerLink, registerServerLinkReceiver } from "@/lib/server-link-queue"
import { cn } from "@/lib/utils"
import { zodResolver } from "@/lib/zod-resolver"

type DetectedServer = {
  serverUrl: string
  brand: Brand
  deploymentName: string
}

/** 检测服务器后确认连接。 */
export function ServerConnectionForm() {
  const { t } = useTranslation(["connection", "common"])
  const navigate = useNavigate()
  const { completeStartup, connectReason, restartStartup } = useStartup()
  const [detected, setDetected] = useState<DetectedServer | null>(null)
  const [detecting, setDetecting] = useState(false)
  const [connecting, setConnecting] = useState(false)
  // 检测请求代次，只采用最近一次检测的结果。
  const detectGeneration = useRef(0)
  const schema = useMemo(() => createServerConnectionSchema(t), [t])
  const form = useForm<ServerConnectionFormValues>({
    resolver: zodResolver(schema),
    shouldUseNativeValidation: true,
    defaultValues: { protocol: "https://", address: "" },
  })
  const { getValues, reset, watch } = form
  const serverUrl = serverConnectionURL({ protocol: watch("protocol"), address: watch("address") })

  useEffect(() => {
    if (detected && detected.serverUrl !== serverUrl) {
      setDetected(null)
    }
  }, [detected, serverUrl])

  const savedUrl = serverURL()
  // 输入框为空时填入已保存的服务器地址。
  useEffect(() => {
    if (!savedUrl || getValues("address").trim() !== "") return
    reset(splitServerURL(savedUrl))
  }, [savedUrl, getValues, reset])

  // 连接链接交来的部署地址填入输入框并立即检测，由用户确认连接。
  const receiveServerLink = useEffectEvent((linkedUrl: string) => {
    const values = splitServerURL(linkedUrl)
    reset(values)
    void detectServer(values)
  })
  useEffect(() => registerServerLinkReceiver((linkedUrl) => receiveServerLink(linkedUrl)), [])

  /** 检测服务器并展示产品名称，仅采用最近一次检测结果；尚未初始化时提示在浏览器中完成安装。 */
  async function detectServer(values: ServerConnectionFormValues) {
    const generation = ++detectGeneration.current
    setDetecting(true)
    try {
      const serverUrl = serverConnectionURL(values)
      const status = await probeServer(serverUrl)
      if (generation !== detectGeneration.current) return null
      const host = new URL(serverUrl).host
      if (!status.installed) {
        setDetected(null)
        toast.error(t("serverNotInstalled", { host }))
        return null
      }
      const server = {
        serverUrl,
        brand: status.brand,
        deploymentName: status.deploymentName.trim() || new URL(serverUrl).host,
      }
      setDetected(server)
      return server
    } catch (error) {
      if (generation !== detectGeneration.current) return null
      setDetected(null)
      if (isApiError(error)) {
        toast.error(apiErrorMessage(error))
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
    const values = splitServerURL(savedUrl)
    reset(values)
    const server = await detectServer(values)
    if (server) await connectDetectedServer(server)
  }

  // 已保存的服务器暂时连不上时，恢复联网后自动重新检测。
  const retryWhenOnline = useEffectEvent(() => void retrySavedServer())
  useEffect(() => {
    if (connectReason !== "unreachable") return
    const onOnline = () => retryWhenOnline()
    window.addEventListener("online", onOnline)
    return () => window.removeEventListener("online", onOnline)
  }, [connectReason])

  /** 保存已检测的服务器并前往登录；服务器要求升级客户端时前往升级页。 */
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
      // 服务器地址已保存，仅本端接口版本过旧时前往升级页并重新启动检测。
      if (isApiError(error) && error.state === SessionState.Upgrade) {
        clearPendingServerLink()
        restartStartup()
        navigate("/upgrade", { replace: true })
        return
      }
      if (isApiError(error)) {
        toast.error(apiErrorMessage(error))
        return
      }
      toast.error(t("connectionError"))
    } finally {
      setConnecting(false)
    }
  }

  const busy = detecting || connecting

  return (
    <>
      {/* 已保存的服务器暂时连不上、尚未完成首次安装或版本过旧时说明原因，连不上时提供重试。 */}
      {savedUrl && connectReason ? (
        <div role="status" className="mb-4 flex items-start gap-3 text-sm/6">
          <p className={cn("min-w-0 flex-1", connectReason === "unreachable" ? "text-destructive" : "text-muted-foreground")}>
            {connectReason === "unreachable"
              ? t("savedServerUnreachable", { serverUrl: savedUrl })
              : connectReason === "server_outdated"
                ? t("serverOutdated", { host: new URL(savedUrl).host })
                : t("serverNotInstalled", { host: new URL(savedUrl).host })}
          </p>
          {connectReason === "unreachable" ? (
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
            <ServerAddressField
              form={form}
              busy={busy}
              detecting={detecting}
              onDetect={() => void form.handleSubmit(detectServer)()}
            />
            {detected ? (
              <div
                className="mt-3 flex items-center justify-between gap-4 rounded-lg bg-muted/50 px-3.5 py-3"
                aria-live="polite"
              >
                <p
                  className="min-w-0 truncate text-[15px] leading-none font-medium tracking-[-0.02em]"
                  title={detected.serverUrl}
                >
                  {detected.deploymentName}
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
        </FieldGroup>
      </form>
    </>
  )
}

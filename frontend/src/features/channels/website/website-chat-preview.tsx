/** 网站渠道挂件预览。 */
import { useCallback, useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import type { WebsiteChannelChatInterfaceInput, WebsiteChannelHomeInput } from "@/api"
import { serverURL } from "@/api/client"
import { LoadingIndicator } from "@/components/loading-indicator"
import { Button } from "@/components/ui/button"

type PreviewStatus = "loading" | "ready" | "failed"

/** 帮助中心表单上报的预览草稿。 */
export type WebsiteHelpCenterPreviewDraft = {
  helpEnabled: boolean
}

/** 访客聊天窗口预览使用的帮助中心设置：渠道编号、帮助页签草稿开关与已保存的发布知识库。 */
type WebsiteHelpCenterPreviewValue = WebsiteHelpCenterPreviewDraft & {
  channelId: string
  helpKnowledgeBaseIds: string[]
}

/** 访客聊天窗口预览使用的聊天窗口、首页与帮助中心合并值。 */
export type WebsiteMessengerPreviewValue = WebsiteChannelChatInterfaceInput &
  WebsiteChannelHomeInput &
  WebsiteHelpCenterPreviewValue

/** 按当前设置预览访客挂件与聊天窗口。 */
export function WebsiteChatPreview({
  value,
}: {
  value: WebsiteMessengerPreviewValue
}) {
  const { t } = useTranslation(["channels", "common"])
  const iframeRef = useRef<HTMLIFrameElement>(null)
  const [status, setStatus] = useState<PreviewStatus>("loading")
  const [retryKey, setRetryKey] = useState(0)
  const previewOrigin = new URL(serverURL()).origin
  const previewURL = `${previewOrigin}/chat/preview`

  /** 同步聊天窗口、首页与帮助中心设置到访客挂件预览。 */
  const syncPreview = useCallback(() => {
    if (!previewOrigin || !iframeRef.current?.contentWindow) return
    iframeRef.current.contentWindow.postMessage(
      { type: "messenger:preview-config", value },
      previewOrigin,
    )
  }, [previewOrigin, value])

  useEffect(() => {
    if (status === "ready") syncPreview()
  }, [status, syncPreview])

  useEffect(() => {
    if (!previewOrigin) return

    /** 只接受当前挂件预览宿主页发出的就绪消息。 */
    function handlePreviewMessage(event: MessageEvent) {
      if (
        event.origin !== previewOrigin ||
        event.source !== iframeRef.current?.contentWindow ||
        event.data?.type !== "messenger:preview-ready"
      ) {
        return
      }
      setStatus("ready")
    }

    window.addEventListener("message", handlePreviewMessage)
    return () => window.removeEventListener("message", handlePreviewMessage)
  }, [previewOrigin])

  useEffect(() => {
    if (!previewURL || status !== "loading") return
    const timeout = window.setTimeout(() => {
      console.warn("网站渠道聊天窗口预览加载超时", {
        preview_url: previewURL,
      })
      setStatus("failed")
    }, 8_000)
    return () => window.clearTimeout(timeout)
  }, [previewURL, status])

  /** 挂件预览页加载后同步当前设置。 */
  function handleLoad() {
    syncPreview()
  }

  /** 记录挂件预览页加载失败。 */
  function handleError() {
    console.warn("网站渠道聊天窗口预览页面加载失败", {
      preview_url: previewURL,
    })
    setStatus("failed")
  }

  /** 重新加载挂件预览。 */
  function retry() {
    setStatus("loading")
    setRetryKey((current) => current + 1)
  }

  return (
    <aside className="w-full max-w-[360px] xl:sticky xl:top-6 xl:self-start">
      <p className="mb-3 text-sm font-medium">
        {t("chatInterface.preview.title")}
      </p>

      {/* 挂件以真实尺寸渲染后整体缩放到预览框内。 */}
      <div className="relative h-[600px] overflow-hidden rounded-2xl border bg-muted/30 shadow-sm">
        {previewURL ? (
          <iframe
            key={retryKey}
            ref={iframeRef}
            className="block h-[800px] w-[480px] origin-top-left scale-75 border-0 bg-background"
            src={previewURL}
            title={t("chatInterface.preview.frameTitle")}
            referrerPolicy="strict-origin-when-cross-origin"
            onLoad={handleLoad}
            onError={handleError}
          />
        ) : null}

        {status !== "ready" ? (
          <div className="absolute inset-0 grid place-items-center bg-background px-8 text-center">
            {status === "failed" ? (
              <div>
                <p className="text-sm text-muted-foreground">
                  {t("chatInterface.preview.loadFailed")}
                </p>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="mt-4"
                  onClick={retry}
                >
                  {t("common:actions.retry")}
                </Button>
              </div>
            ) : (
              <LoadingIndicator>
                {t("chatInterface.preview.loading")}
              </LoadingIndicator>
            )}
          </div>
        ) : null}
      </div>
    </aside>
  )
}

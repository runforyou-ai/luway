/** 企业服务器连接页。 */
import { useLayoutEffect, useRef, useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"

import { useStartup } from "@/contexts/startup-context"
import { ServerConnectionForm } from "@/features/server-connection/server-connection-form"
import { useBrandName } from "@/lib/brand"
import { clearPendingServerLink } from "@/lib/server-link-queue"

/** 按未展开高度垂直居中，内容增高时只向下延伸。 */
function AnchoredCenter({ children }: { children: ReactNode }) {
  const contentRef = useRef<HTMLDivElement>(null)
  const compactHeightRef = useRef<number | null>(null)
  const [offset, setOffset] = useState<number | null>(null)

  useLayoutEffect(() => {
    const node = contentRef.current
    if (!node) {
      return
    }

    const sync = () => {
      const height = node.offsetHeight
      const compact = compactHeightRef.current
      if (compact == null || height <= compact + 1) {
        // 按未展开高度锁定连接页的垂直偏移。
        compactHeightRef.current = height
        setOffset(Math.max(24, (window.innerHeight - height) / 2))
      }
    }

    const onResize = () => {
      const compact = compactHeightRef.current
      if (compact == null) {
        sync()
        return
      }
      setOffset(Math.max(24, (window.innerHeight - compact) / 2))
    }

    sync()
    const observer = new ResizeObserver(sync)
    observer.observe(node)
    window.addEventListener("resize", onResize)
    return () => {
      observer.disconnect()
      window.removeEventListener("resize", onResize)
    }
  }, [])

  return (
    <main
      className={
        offset == null
          ? "flex min-h-dvh w-full items-center justify-center px-6 pt-[max(1.5rem,env(safe-area-inset-top))] pb-[max(1.5rem,env(safe-area-inset-bottom))] md:p-10"
          : "flex min-h-dvh w-full justify-center px-6 pb-[max(1.5rem,env(safe-area-inset-bottom))] md:px-10 md:pb-10"
      }
    >
      <div
        ref={contentRef}
        className="w-full max-w-sm"
        style={offset == null ? undefined : { marginTop: offset }}
      >
        {children}
      </div>
    </main>
  )
}

/** 展示企业服务器地址表单；已连接服务器时可取消切换，回到当前服务器。 */
export function ServerConnectionPage() {
  const { t } = useTranslation("common")
  const navigate = useNavigate()
  const { connected } = useStartup()
  const productName = useBrandName()
  return (
    <AnchoredCenter>
      <div className="mb-6 text-center">
        <p className="text-lg font-semibold tracking-tight">{productName}</p>
      </div>
      <ServerConnectionForm />
      {connected ? (
        <p className="mt-6 text-center">
          <button
            type="button"
            className="text-sm text-muted-foreground transition-colors hover:text-foreground"
            onClick={() => {
              clearPendingServerLink()
              navigate("/", { replace: true })
            }}
          >
            {t("actions.cancel")}
          </button>
        </p>
      ) : null}
    </AnchoredCenter>
  )
}

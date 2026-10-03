/** 登录、注册、首次安装、服务器连接和账号级页面共用的双栏外壳：左侧品牌介绍，右侧页面内容。 */
import { useLayoutEffect, useRef, useState, type ReactNode } from "react"
import { HeadsetIcon, MinusIcon, ShieldCheckIcon, UsersRoundIcon, XIcon } from "lucide-react"
import { Window } from "@wailsio/runtime"
import { useTranslation } from "react-i18next"

import { useBrandName } from "@/lib/brand"
import { cn } from "@/lib/utils"
import { resolveDesktopOS } from "@/platform/app-platform"
import { useDesktopWindowMode } from "@/platform/desktop-window"

/** 页面内容与栏边缘的最小距离。 */
const minContentOffset = 32

/** 渲染入口页：宽屏为品牌栏加内容栏，窄屏为顶部品牌加内容；桌面端主窗口切换为入口小窗口。 */
export function EntryLayout({
  title,
  description,
  leading,
  footer,
  children,
}: {
  title: ReactNode
  description?: ReactNode
  leading?: ReactNode
  footer?: ReactNode
  children: ReactNode
}) {
  const { t } = useTranslation(["entry", "common"])
  const productName = useBrandName()
  const desktopOS = resolveDesktopOS()
  useDesktopWindowMode("entry")

  // 内容按未展开高度垂直居中，内容增高时只向下延伸；窗口尺寸变化时重新测量。
  const columnRef = useRef<HTMLDivElement>(null)
  const contentRef = useRef<HTMLDivElement>(null)
  const compactHeightRef = useRef<number | null>(null)
  const [offset, setOffset] = useState<number | null>(null)
  useLayoutEffect(() => {
    const column = columnRef.current
    const content = contentRef.current
    if (!column || !content) return
    const measure = () => {
      compactHeightRef.current = content.offsetHeight
      setOffset(Math.max(minContentOffset, (column.clientHeight - content.offsetHeight) / 2))
    }
    const sync = () => {
      const compact = compactHeightRef.current
      if (compact == null || content.offsetHeight <= compact + 1) measure()
    }
    sync()
    const observer = new ResizeObserver(sync)
    observer.observe(content)
    window.addEventListener("resize", measure)
    return () => {
      observer.disconnect()
      window.removeEventListener("resize", measure)
    }
  }, [])

  const features = [
    { key: "service", icon: HeadsetIcon },
    { key: "collaboration", icon: UsersRoundIcon },
    { key: "control", icon: ShieldCheckIcon },
  ] as const

  return (
    <main
      className={cn(
        "relative min-h-dvh w-full bg-background",
        desktopOS ? "h-dvh overflow-hidden" : "md:flex md:items-center md:justify-center md:bg-muted/50 md:p-8",
      )}
    >
      <div
        className={cn(
          "flex min-h-dvh w-full flex-col bg-background md:grid md:min-h-0 md:grid-cols-[minmax(0,5fr)_minmax(0,6fr)]",
          desktopOS ? "md:h-dvh" : "md:min-h-[600px] md:max-w-[920px] md:overflow-hidden md:rounded-2xl md:border md:shadow-xl",
        )}
      >
        <aside
          className={cn(
            "app-entry-drag relative hidden flex-col overflow-x-hidden overflow-y-auto bg-primary px-8 py-6 text-primary-foreground md:flex",
            desktopOS === "darwin" && "pt-11",
          )}
        >
          {/* 品牌栏背景装饰。 */}
          <div aria-hidden="true" className="pointer-events-none absolute inset-0 overflow-hidden">
            <div className="absolute -top-24 -right-20 size-72 rounded-full bg-white/10 blur-3xl" />
            <div className="absolute -bottom-28 -left-16 size-80 rounded-full bg-black/10 blur-3xl" />
            <div className="absolute inset-0 bg-[radial-gradient(rgb(255_255_255/0.07)_1px,transparent_1px)] [mask-image:linear-gradient(to_bottom,black,transparent_70%)] bg-[size:18px_18px]" />
          </div>
          <div className="relative">
            <div className="flex items-center gap-3">
              <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-white p-1 shadow-sm">
                <img src="/favicon.png" alt="" className="size-full object-contain" draggable={false} />
              </span>
              <p className="text-lg font-semibold tracking-tight">{productName}</p>
            </div>
            <p className="mt-4 whitespace-pre-line text-[28px] leading-tight font-semibold tracking-tight">{t("entry:headline")}</p>
            <p className="mt-2.5 text-sm/6 text-primary-foreground/85">{t("entry:description")}</p>
          </div>
          <ul className="relative mt-6 space-y-3">
            {features.map(({ key, icon: Icon }) => (
              <li key={key} className="flex gap-2.5">
                <span className="flex size-7 shrink-0 items-center justify-center rounded-lg bg-white/10">
                  <Icon className="size-4" />
                </span>
                <span className="min-w-0">
                  <span className="block text-sm/5 font-medium">{t(`entry:features.${key}.title`)}</span>
                  <span className="mt-0.5 block text-sm/5 text-primary-foreground/80">{t(`entry:features.${key}.description`)}</span>
                </span>
              </li>
            ))}
          </ul>
        </aside>
        <div
          ref={columnRef}
          className="relative flex min-h-0 flex-1 flex-col overflow-y-auto px-6 pt-[env(safe-area-inset-top)] pb-[max(1.5rem,env(safe-area-inset-bottom))] md:px-12 md:pt-0 md:pb-8"
        >
          <div
            ref={contentRef}
            className={cn("mx-auto w-full max-w-sm", offset == null && "my-auto")}
            style={offset == null ? undefined : { marginTop: offset }}
          >
            <div className="mb-10 flex flex-col items-center text-center md:hidden">
              <span className="flex size-12 items-center justify-center rounded-2xl border bg-background p-1.5 shadow-sm">
                <img src="/favicon.png" alt="" className="size-full object-contain" draggable={false} />
              </span>
              <p className="mt-3 text-lg font-semibold tracking-tight">{productName}</p>
              <p className="mt-1 text-sm/6 text-muted-foreground">{t("entry:compactTagline")}</p>
            </div>
            <div className="mb-7">
              {leading}
              <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
              {description ? <p className="mt-1.5 text-sm/6 text-muted-foreground">{description}</p> : null}
            </div>
            {children}
            {footer ? <div className="mt-8 text-center text-sm text-muted-foreground">{footer}</div> : null}
          </div>
        </div>
      </div>
      {/* Windows 与 Linux 的入口小窗口无系统边框，顶部留出拖动区域并提供最小化和关闭按钮。 */}
      {desktopOS && desktopOS !== "darwin" ? (
        <div className="absolute inset-x-0 top-0 z-50 flex h-8 md:left-[calc(5/11*100%)]">
          <div className="app-entry-drag flex-1" />
          <button
            type="button"
            className="flex w-11 items-center justify-center text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            aria-label={t("common:actions.minimize")}
            title={t("common:actions.minimize")}
            onClick={() => void Window.Minimise()}
          >
            <MinusIcon className="size-4" />
          </button>
          <button
            type="button"
            className="flex w-11 items-center justify-center text-muted-foreground transition-colors hover:bg-destructive hover:text-white"
            aria-label={t("common:actions.close")}
            title={t("common:actions.close")}
            onClick={() => void Window.Close()}
          >
            <XIcon className="size-4" />
          </button>
        </div>
      ) : null}
    </main>
  )
}

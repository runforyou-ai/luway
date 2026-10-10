/** Windows 与 Linux 共用的右侧窗口控制按钮。 */
import { useEffect, useState } from "react"
import { Events, Window } from "@wailsio/runtime"
import { CopyIcon, MinusIcon, SquareIcon, XIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { resolveDesktopOS } from "@/platform/app-platform"

/** 渲染最小化、最大化或还原、关闭按钮，macOS 使用原生窗口按钮。 */
export function DesktopWindowControls({ maximizable = true }: { maximizable?: boolean }) {
  const { t } = useTranslation("common")
  const os = resolveDesktopOS()
  const enabled = os === "windows" || os === "linux"
  const [maximized, setMaximized] = useState(false)

  useEffect(() => {
    if (!enabled || !maximizable) return
    let active = true
    let revision = 0
    // 原生事件到达后，以最新窗口状态覆盖初次读取结果。
    const sync = () => {
      const currentRevision = ++revision
      void Window.IsMaximised().then((value) => {
        if (active && currentRevision === revision) setMaximized(value)
      }).catch((error: unknown) => console.warn("读取窗口最大化状态失败", error))
    }
    sync()
    const stops = [
      Events.Types.Common.WindowMaximise,
      Events.Types.Common.WindowUnMaximise,
      Events.Types.Common.WindowRestore,
    ].map((event) => Events.On(event, sync))
    return () => {
      active = false
      for (const stop of stops) stop()
    }
  }, [enabled, maximizable])

  if (!enabled) return null
  const maximizeLabel = t(maximized ? "actions.restoreWindow" : "actions.maximize")

  return (
    <div className="app-window-controls">
      <button
        type="button"
        className="app-window-control"
        data-window-control="minimize"
        aria-label={t("actions.minimize")}
        title={t("actions.minimize")}
        onClick={() => void Window.Minimise()}
      >
        <MinusIcon aria-hidden="true" />
      </button>
      {maximizable ? (
        <button
          type="button"
          className="app-window-control"
          data-window-control="maximize"
          aria-label={maximizeLabel}
          title={maximizeLabel}
          onClick={() => void Window.ToggleMaximise()}
        >
          {maximized ? <CopyIcon aria-hidden="true" /> : <SquareIcon aria-hidden="true" />}
        </button>
      ) : null}
      <button
        type="button"
        className="app-window-control"
        data-window-control="close"
        aria-label={t("actions.close")}
        title={t("actions.close")}
        onClick={() => void Window.Close()}
      >
        <XIcon aria-hidden="true" />
      </button>
    </div>
  )
}

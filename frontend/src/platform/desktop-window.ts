/** 桌面端主窗口在入口小窗口与工作台窗口之间切换尺寸与边框，并在首个模式就绪后显示。 */
import { useEffect } from "react"
import { Window } from "@wailsio/runtime"

import { resolveDesktopOS } from "@/platform/app-platform"

/** 主窗口模式：入口页使用固定尺寸小窗口，工作台使用可缩放大窗口。 */
export type DesktopWindowMode = "entry" | "workspace"

/** 主窗口名称，与原生端创建主窗口时的名称一致。 */
const mainWindowName = "main"

/** 入口小窗口尺寸。 */
const entryWindowSize = { width: 860, height: 580 }

/** 工作台窗口的默认与最小尺寸，与原生端创建主窗口时的尺寸一致。 */
const workspaceWindowSize = { width: 1440, height: 900 }

// 主窗口以工作台尺寸隐藏创建，模式调用按顺序执行。
let currentMode: DesktopWindowMode = "workspace"
let revealed = false
let queue: Promise<void> = Promise.resolve()

/** 把主窗口切到指定模式；macOS 保留系统窗口按钮，其他系统的入口小窗口去掉边框。 */
async function applyMode(mode: DesktopWindowMode) {
  if ((await Window.Name()) !== mainWindowName) return
  const os = resolveDesktopOS()
  if (mode !== currentMode) {
    if (mode === "entry") {
      // 全屏或最大化时先还原窗口，尺寸调整才会生效。
      if (await Window.IsFullscreen()) {
        await Window.UnFullscreen()
        for (let attempt = 0; attempt < 40 && (await Window.IsFullscreen()); attempt += 1) {
          await new Promise((resolve) => window.setTimeout(resolve, 50))
        }
      }
      if (await Window.IsMaximised()) await Window.UnMaximise()
      await Window.SetMinSize(entryWindowSize.width, entryWindowSize.height)
      await Window.SetSize(entryWindowSize.width, entryWindowSize.height)
      await Window.SetResizable(false)
      if (os !== "darwin") await Window.SetFrameless(true)
    } else {
      if (os !== "darwin") await Window.SetFrameless(false)
      await Window.SetResizable(true)
      await Window.SetSize(workspaceWindowSize.width, workspaceWindowSize.height)
      await Window.SetMinSize(workspaceWindowSize.width, workspaceWindowSize.height)
    }
    await Window.Center()
    currentMode = mode
  }
  if (!revealed) {
    revealed = true
    await Window.Show()
    await Window.Focus()
  }
}

/** 按顺序执行主窗口调整。 */
function enqueue(task: () => Promise<void>) {
  queue = queue.then(task).catch((error: unknown) => {
    console.warn("调整主窗口失败", error)
  })
}

/** 组件挂载时把桌面端主窗口切到指定模式，其他平台和其他窗口不处理。 */
export function useDesktopWindowMode(mode: DesktopWindowMode) {
  useEffect(() => {
    if (resolveDesktopOS()) enqueue(() => applyMode(mode))
  }, [mode])
}

/** 主窗口在等待时长内仍未显示时，按当前地址选择入口或工作台模式并显示。 */
export function scheduleDesktopWindowReveal(delayMs: number) {
  if (!resolveDesktopOS()) return
  window.setTimeout(() => {
    if (revealed) return
    enqueue(() => applyMode(window.location.hash.startsWith("#/w/") ? "workspace" : "entry"))
  }, delayMs)
}

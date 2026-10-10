/** Windows 标题菜单的窗口操作、编辑命令与快捷键。 */
import { Application, Clipboard, Window } from "@wailsio/runtime"

/** 菜单命令作用于当前 WebView 所属窗口。 */
export const desktopMenuActions = {
  close: () => Window.Close(),
  quit: () => Application.Quit(),
  reload: () => Window.Reload(),
  forceReload: () => Window.ForceReload(),
  resetZoom: () => Window.ZoomReset(),
  zoomIn: () => Window.ZoomIn(),
  zoomOut: () => Window.ZoomOut(),
  fullscreen: () => Window.ToggleFullscreen(),
  minimize: () => Window.Minimise(),
  maximize: () => Window.ToggleMaximise(),
}

export type DesktopMenuAction = keyof typeof desktopMenuActions
export type DesktopEditAction = "undo" | "redo" | "cut" | "copy" | "paste" | "selectAll"

/** 在当前 WebView 的原输入控件上执行编辑操作，保留浏览器撤销历史。 */
export async function executeDesktopEdit(action: DesktopEditAction) {
  const target = document.activeElement
  if (action === "selectAll" && (target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement)) {
    target.select()
    return
  }
  if (action === "paste") {
    if (!(target instanceof HTMLElement) || !(target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target.isContentEditable)) return
    const data = new DataTransfer()
    // 原生剪贴板提供纯文本，富文本编辑器通过粘贴事件处理正文。
    const text = await Clipboard.Text()
    if (!target.isConnected || document.activeElement !== target) return
    data.setData("text/plain", text)
    const event = new ClipboardEvent("paste", { bubbles: true, cancelable: true, clipboardData: data })
    if (target.dispatchEvent(event)) document.execCommand("insertText", false, text)
    return
  }
  document.execCommand(action)
}

/** 解析 Windows 应用菜单快捷键，文本编辑快捷键由输入控件处理。 */
export function desktopMenuShortcut(event: KeyboardEvent): DesktopMenuAction | undefined {
  if (event.isComposing || event.altKey || event.metaKey) return
  if (event.key === "F11" && !event.ctrlKey && !event.shiftKey) return "fullscreen"
  if (!event.ctrlKey) return
  if (event.key.toLowerCase() === "r") return event.shiftKey ? "forceReload" : "reload"
  if (event.key === "+" || event.key === "=") return "zoomIn"
  if (event.shiftKey) return
  return ({ w: "close", q: "quit", "0": "resetZoom", "-": "zoomOut", m: "minimize" } as const)[
    event.key.toLowerCase() as "w" | "q" | "0" | "-" | "m"
  ]
}

/** Windows 窗口顶栏文字菜单，保留编辑焦点并调用当前窗口的原生能力。 */
import { useEffect, useLayoutEffect, useRef, useState } from "react"
import { Menubar } from "radix-ui"
import { useTranslation } from "react-i18next"

import { resolveDesktopOS } from "@/platform/app-platform"
import { desktopMenuActions, desktopMenuShortcut, executeDesktopEdit, type DesktopEditAction, type DesktopMenuAction } from "@/platform/desktop-menu"

const menuGroups = [
  { id: "file", key: "f", items: [["close", "Ctrl+W"], ["quit", "Ctrl+Q"]] },
  { id: "edit", key: "e", items: [["undo", "Ctrl+Z"], ["redo", "Ctrl+Shift+Z"], null, ["cut", "Ctrl+X"], ["copy", "Ctrl+C"], ["paste", "Ctrl+V"], null, ["selectAll", "Ctrl+A"]] },
  { id: "view", key: "v", items: [["reload", "Ctrl+R"], ["forceReload", "Ctrl+Shift+R"], null, ["resetZoom", "Ctrl+0"], ["zoomIn", "Ctrl++"], ["zoomOut", "Ctrl+-"], null, ["fullscreen", "F11"]] },
  { id: "window", key: "w", items: [["minimize", "Ctrl+M"], ["maximize", ""]] },
] as const

/** 渲染当前窗口的文件、编辑、显示与窗口菜单，固定尺寸窗口禁用缩放窗口操作。 */
export function DesktopMenu({ maximizable = true }: { maximizable?: boolean }) {
  const { t } = useTranslation("common")
  const enabled = resolveDesktopOS() === "windows"
  const root = useRef<HTMLDivElement>(null)
  const target = useRef<HTMLElement | null>(null)
  const selection = useRef<Range[]>([])
  const inputSelection = useRef<{ start: number; end: number; direction: "forward" | "backward" | "none" } | null>(null)
  const restoreOnClose = useRef(true)
  const [value, setValue] = useState("")
  const openMenu = useRef(value)
  openMenu.current = value

  // 原生拖动矩形从菜单末端开始，宽度随语言与页面缩放同步。
  useLayoutEffect(() => {
    const menu = root.current
    const shell = menu?.closest<HTMLElement>(".app-workspace-shell, .app-conversation-window")
    if (!enabled || !menu || !shell) return
    const measure = () => shell.style.setProperty("--app-titlebar-menu-end", `${menu.getBoundingClientRect().right - shell.getBoundingClientRect().left}px`)
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(menu)
    window.addEventListener("resize", measure)
    return () => { observer.disconnect(); window.removeEventListener("resize", measure); shell.style.removeProperty("--app-titlebar-menu-end") }
  }, [enabled])

  // 焦点进入菜单前保存文本选区，菜单关闭后恢复原输入位置。
  useEffect(() => {
    if (!enabled) return
    const remember = () => {
      const active = document.activeElement
      if (!(active instanceof HTMLElement) || active.closest("[data-desktop-menu]")) return
      target.current = active
      inputSelection.current = (active instanceof HTMLInputElement || active instanceof HTMLTextAreaElement) && active.selectionStart !== null
        ? { start: active.selectionStart, end: active.selectionEnd ?? active.selectionStart, direction: active.selectionDirection ?? "none" } : null
      const selected = window.getSelection()
      selection.current = selected ? Array.from({ length: selected.rangeCount }, (_, index) => selected.getRangeAt(index).cloneRange()) : []
    }
    const handleKey = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing) return
      remember()
      const group = event.altKey && !event.ctrlKey && !event.shiftKey && !event.metaKey
        ? menuGroups.find((item) => item.key === event.key.toLowerCase()) : undefined
      if (group || (event.key === "F10" && !event.shiftKey && !event.ctrlKey && !event.altKey)) {
        event.preventDefault()
        remember()
        if (group) { restoreOnClose.current = true; setValue(group.id) }
        else root.current?.querySelector<HTMLButtonElement>("button")?.focus()
        return
      }
      const action = desktopMenuShortcut(event)
      if (!action || (!maximizable && action === "fullscreen")) return
      event.preventDefault()
      void desktopMenuActions[action]().catch((error: unknown) => console.warn("执行窗口菜单失败", error))
    }
    document.addEventListener("pointerdown", remember, true)
    document.addEventListener("keydown", handleKey)
    return () => {
      document.removeEventListener("pointerdown", remember, true)
      document.removeEventListener("keydown", handleKey)
    }
  }, [enabled, maximizable])

  /** 恢复菜单打开前的焦点与正文选区。 */
  function restoreFocus() {
    if (!target.current?.isConnected) return
    target.current.focus({ preventScroll: true })
    if (inputSelection.current && (target.current instanceof HTMLInputElement || target.current instanceof HTMLTextAreaElement)) {
      const { start, end, direction } = inputSelection.current
      target.current.setSelectionRange(start, end, direction)
      return
    }
    const selected = window.getSelection()
    selected?.removeAllRanges()
    selection.current.filter((range) => range.commonAncestorContainer.isConnected).forEach((range) => {
      selected?.addRange(range)
    })
  }

  if (!enabled) return null
  return (
    <Menubar.Root ref={root} data-desktop-menu="" className="app-desktop-menu" value={value} onValueChange={(next) => { if (next) restoreOnClose.current = true; setValue(next) }} aria-label={t("desktopMenu.label")}>
      {menuGroups.map((group) => (
        <Menubar.Menu key={group.id} value={group.id}>
          <Menubar.Trigger className="app-desktop-menu-trigger" aria-keyshortcuts={`Alt+${group.key}`}>{t(`desktopMenu.${group.id}`)}</Menubar.Trigger>
          <Menubar.Portal>
            <Menubar.Content data-desktop-menu="" className="app-desktop-menu-content" align="start" sideOffset={3}
              onInteractOutside={() => { restoreOnClose.current = false }}
              onCloseAutoFocus={(event) => { event.preventDefault(); if (restoreOnClose.current && !openMenu.current) restoreFocus() }}>
              {group.items.map((item, index) => item === null ? <Menubar.Separator key={index} className="my-1 h-px bg-border" /> : (
                <Menubar.Item key={item[0]} className="app-desktop-menu-item"
                  disabled={!maximizable && (item[0] === "maximize" || item[0] === "fullscreen")}
                  onSelect={() => {
                    restoreFocus()
                    restoreOnClose.current = false
                    if (item[0] in desktopMenuActions) {
                      void desktopMenuActions[item[0] as DesktopMenuAction]().catch((error: unknown) => console.warn("执行窗口菜单失败", error))
                    } else void executeDesktopEdit(item[0] as DesktopEditAction).catch((error: unknown) => console.warn("执行编辑菜单失败", error))
                  }}>
                  <span>{t(item[0] === "undo" || item[0] === "copy" || item[0] === "selectAll" || item[0] === "minimize" ? `actions.${item[0]}` : `desktopMenu.${item[0]}`)}</span>
                  <span className="ml-8 text-xs text-muted-foreground">{item[1]}</span>
                </Menubar.Item>
              ))}
            </Menubar.Content>
          </Menubar.Portal>
        </Menubar.Menu>
      ))}
    </Menubar.Root>
  )
}

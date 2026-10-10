/** 保存列表原邻域，在布局变化和触摸结束后统一补偿滚动位置。 */
import { useEffect, useLayoutEffect, useMemo, useRef } from "react"
import type { InboxListAnchor, InboxListState } from "./inbox-list-controller"

/** 为原生移动滚动区和桌面 ScrollArea 提供同一锚点与交互生命周期。 */
export function useInboxListViewport() {
  const root = useRef<HTMLDivElement>(null)
  const positions = useRef<InboxListState["positions"]>([])
  const lastAnchor = useRef<InboxListAnchor | null>(null)
  const pending = useRef<{ anchor: InboxListAnchor | null; moved: Set<string>; top: boolean } | null>(null)
  const interaction = useRef({ scrollingUntil: 0, pointer: false, menu: false, covered: false, keyboard: false, dragging: false })
  const events = useRef({ idle: () => {}, scroll: (_element: HTMLElement, _enteredTop: boolean) => {} })
  const wake = useRef(() => {})
  const programmatic = useRef<number | null>(null)
  const viewport = useMemo(() => ({
    root, positions, interaction, events,
    /** 返回当前可测量的滚动容器。 */
    element: () => root.current?.querySelector<HTMLElement>("[data-inbox-viewport], [data-slot=scroll-area-viewport]") ?? null,
    /** 捕获稳定可见行及原后继、前驱，隐藏时保留上次可见邻域。 */
    capture: (): InboxListAnchor | null => {
      const container = viewport.element()
      if (!container?.clientHeight) return lastAnchor.current
      const top = container.getBoundingClientRect().top
      const rows = [...container.querySelectorAll<HTMLElement>("[data-inbox-id]")]
      const first = rows.findIndex((row) => row.getBoundingClientRect().bottom > top)
      if (first < 0) return lastAnchor.current
      const id = rows[first].dataset.inboxId!
      const neighbors = [...rows.slice(first), ...rows.slice(0, first).reverse()].map((row) => ({ id: row.dataset.inboxId!, offset: row.getBoundingClientRect().top - top }))
      lastAnchor.current = { id, width: container.clientWidth, height: container.clientHeight, cursor: positions.current.find((row) => row.id === id)?.positionCursor ?? "", neighbors }
      return lastAnchor.current
    },
    atTop: (): boolean => (viewport.element()?.scrollTop ?? 0) <= 2,
    interacting: () => interaction.current.pointer || interaction.current.menu || interaction.current.covered || interaction.current.keyboard || interaction.current.dragging || performance.now() < interaction.current.scrollingUntil,
    /** 登记最新的恢复意图；尚未应用的已移动行并入本次，两个分区的重排共用一次补偿。 */
    restore: (anchor: InboxListAnchor | null, moved: Set<string>, top: boolean) => { pending.current = { anchor, moved: new Set([...(pending.current?.moved ?? []), ...moved]), top } },
    /** 菜单和窄屏详情打开时保护底层列表，关闭后恢复顺序。 */
    setMenu: (open: boolean) => { interaction.current.menu = open; wake.current() },
    /** 置顶排序拖动期间保护列表顺序，结束后恢复。 */
    setDragging: (active: boolean) => { interaction.current.dragging = active; wake.current() },
    /** 窄屏详情覆盖时保留底层列表原位。 */
    setCovered: (open: boolean) => { interaction.current.covered = open; wake.current() },
  }), [])

  /** 布局提交后补偿一次，触摸和隐藏期间只保留恢复意图。 */
  function apply() {
    const intent = pending.current
    const container = viewport.element()
    if (!intent || !container?.clientHeight || viewport.interacting()) return
    pending.current = null
    const previousTop = container.scrollTop
    if (intent.top) container.scrollTop = 0
    else {
      const top = container.getBoundingClientRect().top
      const rows = new Map([...container.querySelectorAll<HTMLElement>("[data-inbox-id]")].map((row) => [row.dataset.inboxId!, row]))
      const neighbor = intent.anchor?.neighbors.find((row) => !intent.moved.has(row.id) && rows.has(row.id))
      if (neighbor) {
        const bounds = rows.get(neighbor.id)!.getBoundingClientRect()
        // 尺寸改变后调整负偏移，使锚点行至少保留一像素可见高度。
        const resized = intent.anchor?.width !== container.clientWidth || intent.anchor?.height !== container.clientHeight
        const offset = resized ? Math.max(neighbor.offset, 1 - bounds.height) : neighbor.offset
        container.scrollTop += bounds.top - top - offset
      }
    }
    if (container.scrollTop !== previousTop) programmatic.current = container.scrollTop
    viewport.capture()
  }
  const applyRef = useRef(apply)
  applyRef.current = apply

  useEffect(() => {
    const container = viewport.element()
    const host = root.current
    if (!container || !host) return
    let timer = 0
    let nearTop = container.scrollTop <= 120
    let size = { width: container.clientWidth, height: container.clientHeight, content: container.scrollHeight }
    /** 等待滚动、指针、键盘和浮层操作结束，再应用顺序与恢复位置。 */
    function settle() {
      window.clearTimeout(timer)
      timer = window.setTimeout(() => {
        const input = interaction.current
        if (input.pointer || input.menu || input.covered || input.keyboard || input.dragging) return
        if (viewport.interacting()) { settle(); return }
        applyRef.current()
        events.current.idle()
      }, 180)
    }
    wake.current = settle
    /** 用户滚动更新锚点；程序补偿产生的 scroll 不开启新的交互周期。 */
    function scroll() {
      if (programmatic.current === container!.scrollTop) { programmatic.current = null; return }
      programmatic.current = null
      interaction.current.scrollingUntil = performance.now() + 180
      // 用户继续浏览时按当前位置更新阅读意图。
      pending.current = null
      viewport.capture()
      const nextNearTop = container!.scrollTop <= 120
      events.current.scroll(container!, nextNearTop && !nearTop)
      nearTop = nextNearTop
      settle()
    }
    /** 指针按下后保持命中行，松手与惯性停止后才允许重排。 */
    function down() { interaction.current.pointer = true; settle() }
    /** 指针释放后等待滚动平稳，再应用列表重排。 */
    function release() { interaction.current.pointer = false; settle() }
    /** 键盘操作期间保持焦点行的位置。 */
    function keydown() { interaction.current.keyboard = true; settle() }
    /** 按键释放后恢复列表布局。 */
    function keyup() { interaction.current.keyboard = false; settle() }
    /** 窗口失焦时释放物理输入，浮层保护仍由调用方控制。 */
    function blur() { interaction.current.pointer = false; interaction.current.keyboard = false; settle() }
    container.addEventListener("scroll", scroll, { passive: true })
    host.addEventListener("pointerdown", down, true)
    host.addEventListener("keydown", keydown, true)
    window.addEventListener("pointerup", release)
    window.addEventListener("pointercancel", release)
    window.addEventListener("keyup", keyup)
    window.addEventListener("blur", blur)
    // ResizeObserver 在旋转、字体或隐藏容器重新可见后，以旧邻域重测实际行高。
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(() => {
      const next = { width: container.clientWidth, height: container.clientHeight, content: container.scrollHeight }
      if (next.width === size.width && next.height === size.height && next.content === size.content) return
      size = next
      if (!pending.current && lastAnchor.current) viewport.restore(lastAnchor.current, new Set(), false)
      applyRef.current()
      settle()
    })
    observer?.observe(container)
    if (container.firstElementChild) observer?.observe(container.firstElementChild)
    return () => {
      window.clearTimeout(timer)
      observer?.disconnect()
      wake.current = () => {}
      container.removeEventListener("scroll", scroll)
      host.removeEventListener("pointerdown", down, true)
      host.removeEventListener("keydown", keydown, true)
      window.removeEventListener("pointerup", release)
      window.removeEventListener("pointercancel", release)
      window.removeEventListener("keyup", keyup)
      window.removeEventListener("blur", blur)
    }
  }, [viewport])
  useLayoutEffect(() => { apply(); if (!pending.current) viewport.capture() })
  return viewport
}

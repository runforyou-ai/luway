/** 在真实 React 提交周期中验证列表唯一滚动补偿与原邻居优先级。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import { JSDOM } from "jsdom"
import { act, createElement } from "react"
import { createRoot } from "react-dom/client"
import { useInboxListViewport } from "../src/features/inbox/use-inbox-list-viewport.ts"

test("上方插入、锚点移走、后继消失时仅补偿一次，并保留原像素偏移", async () => {
  const dom = new JSDOM('<div id="root"></div>')
  const oldWindow = globalThis.window
  const oldDocument = globalThis.document
  Object.assign(globalThis, { window: dom.window, document: dom.window.document, IS_REACT_ACT_ENVIRONMENT: true })
  let viewport: ReturnType<typeof useInboxListViewport>
  /** 挂载与页面一致的视口适配器，使用浏览器布局测量夹具。 */
  function Harness() {
    viewport = useInboxListViewport()
    return createElement("div", { ref: viewport.root }, createElement("div", { "data-slot": "scroll-area-viewport" }))
  }
  const root = createRoot(document.getElementById("root")!)
  try {
    await act(async () => root.render(createElement(Harness)))
    const container = document.querySelector<HTMLElement>('[data-slot="scroll-area-viewport"]')!
    Object.defineProperty(container, "clientHeight", { configurable: true, value: 100 })
    container.getBoundingClientRect = () => ({ top: 100, bottom: 200 } as DOMRect)
    let ids = ["79", "80", "81", "82"]
    // 以行序和 scrollTop 模拟布局，检查的是最终滚动偏移。
    for (const id of ["new", ...ids]) {
      const row = document.createElement("button")
      row.dataset.inboxId = id
      row.getBoundingClientRect = () => ({ top: 100 + ids.indexOf(id) * 68 - container.scrollTop, bottom: 168 + ids.indexOf(id) * 68 - container.scrollTop } as DOMRect)
      if (id !== "new") container.append(row)
    }
    viewport!.positions.current = ids.map((id) => ({ id, positionCursor: `p${id}`, lastActivityAt: null }))
    container.scrollTop = 78
    const anchor = viewport!.capture()!
    assert.equal(anchor.id, "80")
    assert.deepEqual(anchor.neighbors.slice(0, 2), [{ id: "80", offset: -10 }, { id: "81", offset: 58 }])
    ids = ["new", ...ids]
    viewport!.restore(anchor, new Set(), false)
    await act(async () => root.render(createElement(Harness)))
    assert.equal(container.scrollTop, 146)
    await act(async () => root.render(createElement(Harness)))
    assert.equal(container.scrollTop, 146)
    ids = ["80", "new", "79", "81", "82"]
    viewport!.restore(anchor, new Set(["80"]), false)
    await act(async () => root.render(createElement(Harness)))
    assert.equal(container.scrollTop, 146)
    container.querySelector('[data-inbox-id="81"]')!.remove()
    container.querySelector('[data-inbox-id="82"]')!.remove()
    ids = ["80", "new", "79"]
    viewport!.restore(anchor, new Set(["80"]), false)
    await act(async () => root.render(createElement(Harness)))
    assert.equal(container.scrollTop, 214)
    Object.defineProperty(container, "clientHeight", { configurable: true, value: 0 })
    viewport!.restore(null, new Set(), true)
    await act(async () => root.render(createElement(Harness)))
    assert.equal(container.scrollTop, 214)
    Object.defineProperty(container, "clientHeight", { configurable: true, value: 100 })
    await act(async () => root.render(createElement(Harness)))
    assert.equal(container.scrollTop, 0)
  } finally {
    await act(async () => root.unmount())
    Object.assign(globalThis, { window: oldWindow, document: oldDocument })
    dom.window.close()
  }
})

test("指针取消后的惯性滚动延后恢复，旋转和字体变化按行位置重测", async () => {
  const dom = new JSDOM('<div id="root"></div>')
  const previous = { window: globalThis.window, document: globalThis.document, ResizeObserver: globalThis.ResizeObserver }
  Object.assign(globalThis, { window: dom.window, document: dom.window.document, IS_REACT_ACT_ENVIRONMENT: true })
  let resize = () => {}
  const timers = new Map<number, () => void>()
  let timerID = 0
  let now = performance.now()
  const originalNow = performance.now
  performance.now = () => now
  window.setTimeout = ((callback: () => void) => { timers.set(++timerID, callback); return timerID }) as typeof window.setTimeout
  window.clearTimeout = (id) => { timers.delete(id) }
  globalThis.ResizeObserver = class {
    constructor(callback: () => void) { resize = callback }
    observe() {}
    unobserve() {}
    disconnect() {}
  } as typeof ResizeObserver
  let viewport: ReturnType<typeof useInboxListViewport>
  /** 使用原生移动滚动容器验证交互和尺寸通知。 */
  function Harness() {
    viewport = useInboxListViewport()
    return createElement("div", { ref: viewport.root }, createElement("div", { "data-inbox-viewport": true }, createElement("div")))
  }
  const root = createRoot(document.getElementById("root")!)
  try {
    await act(async () => root.render(createElement(Harness)))
    const container = viewport!.element()!
    let width = 390
    let rowHeight = 68
    Object.defineProperties(container, {
      clientWidth: { get: () => width }, clientHeight: { value: 150 }, scrollHeight: { get: () => rowHeight * 20 },
    })
    container.getBoundingClientRect = () => ({ top: 0, bottom: 150 } as DOMRect)
    for (let index = 0; index < 20; index++) {
      const row = document.createElement("button")
      row.dataset.inboxId = String(index)
      row.getBoundingClientRect = () => ({ top: index * rowHeight - container.scrollTop, bottom: (index + 1) * rowHeight - container.scrollTop, height: rowHeight } as DOMRect)
      container.firstElementChild!.append(row)
    }
    container.scrollTop = 350
    const anchor = viewport!.capture()!
    assert.equal(anchor.id, "5")
    viewport!.root.current!.dispatchEvent(new window.Event("pointerdown", { bubbles: true }))
    viewport!.restore(anchor, new Set(), false)
    rowHeight = 80
    await act(async () => root.render(createElement(Harness)))
    assert.equal(container.scrollTop, 350)
    window.dispatchEvent(new window.Event("pointercancel"))
    container.scrollTop = 430
    container.dispatchEvent(new window.Event("scroll"))
    assert.equal(viewport!.interacting(), true)
    let idle = 0
    viewport!.events.current.idle = () => { idle++ }
    now += 181
    await act(async () => { for (const [id, callback] of [...timers]) { timers.delete(id); callback() } })
    assert.equal(idle, 1)
    assert.equal(container.scrollTop, 430)
    const currentAnchor = viewport!.capture()!
    width = 844
    rowHeight = 60
    await act(async () => resize())
    assert.equal(container.scrollTop, 330)
    assert.equal(viewport!.capture()!.id, currentAnchor.id)
    assert.equal(viewport!.capture()!.neighbors[0].offset, currentAnchor.neighbors[0].offset)
    await act(async () => resize())
    assert.equal(container.scrollTop, 330)
    // 菜单、覆盖层和键盘关闭后各自唤醒一次恢复，操作中不改位。
    for (const kind of ["menu", "covered", "keyboard"] as const) {
      container.scrollTop = 330
      if (kind === "menu") viewport!.setMenu(true)
      else if (kind === "covered") viewport!.setCovered(true)
      else viewport!.root.current!.dispatchEvent(new window.Event("keydown", { bubbles: true }))
      viewport!.restore(null, new Set(), true)
      now += 181
      await act(async () => { for (const [id, callback] of [...timers]) { timers.delete(id); callback() } })
      assert.equal(container.scrollTop, 330)
      if (kind === "menu") viewport!.setMenu(false)
      else if (kind === "covered") viewport!.setCovered(false)
      else window.dispatchEvent(new window.Event("keyup"))
      now += 181
      await act(async () => { for (const [id, callback] of [...timers]) { timers.delete(id); callback() } })
      assert.equal(container.scrollTop, 0)
    }
    const edges: boolean[] = []
    viewport!.events.current.scroll = (_element, entered) => { edges.push(entered) }
    for (const offset of [500, 100, 20, 130, 80]) {
      container.scrollTop = offset
      container.dispatchEvent(new window.Event("scroll"))
    }
    assert.deepEqual(edges, [false, true, false, false, true])
  } finally {
    await act(async () => root.unmount())
    performance.now = originalNow
    Object.assign(globalThis, previous)
    dom.window.close()
  }
})

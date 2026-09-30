/** 用假观察器与假节点验证已读观察器的创建、增量观察和释放。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import { MessageReadObserver } from "../src/features/inbox/message-read-observer.ts"

type FakeNode = Element & { isConnected: boolean }

/** 构造可控节点列表的视口与记录调用的观察器工厂。 */
function setup() {
  const nodes: FakeNode[] = []
  const viewport = { querySelectorAll: () => nodes } as unknown as ParentNode & Element
  const observers: { observed: Set<Element>; disconnected: boolean }[] = []
  const reader = new MessageReadObserver(() => undefined, () => {
    const record = { observed: new Set<Element>(), disconnected: false }
    observers.push(record)
    return {
      observe: (node: Element) => void record.observed.add(node),
      unobserve: (node: Element) => void record.observed.delete(node),
      disconnect: () => {
        record.disconnected = true
        record.observed.clear()
      },
    }
  })
  const node = () => {
    const next = { isConnected: true } as FakeNode
    nodes.push(next)
    return next
  }
  return { reader, viewport, nodes, observers, node }
}

test("视口首次就绪时创建观察器并观察已有节点", () => {
  const { reader, viewport, observers, node } = setup()
  reader.sync(null, false)
  assert.equal(observers.length, 0)
  node()
  node()
  reader.sync(viewport, true)
  assert.equal(observers.length, 1)
  assert.equal(observers[0].observed.size, 2)
})

test("窗口变化时沿用观察器，只观察新节点并释放已移除节点", () => {
  const { reader, viewport, nodes, observers, node } = setup()
  const first = node()
  reader.sync(viewport, true)
  first.isConnected = false
  nodes.shift()
  const second = node()
  reader.sync(viewport, true)
  assert.equal(observers.length, 1)
  assert.deepEqual([...observers[0].observed], [second])
})

test("不可推进已读时释放观察器，恢复后新建观察器重新观察全部节点", () => {
  const { reader, viewport, observers, node } = setup()
  node()
  reader.sync(viewport, true)
  reader.sync(viewport, false)
  assert.equal(observers[0].disconnected, true)
  assert.equal(observers[0].observed.size, 0)
  reader.sync(viewport, true)
  assert.equal(observers.length, 2)
  assert.equal(observers[1].observed.size, 1)
})

test("视口替换时释放旧观察器", () => {
  const { reader, viewport, observers, node } = setup()
  node()
  reader.sync(viewport, true)
  const replaced = { querySelectorAll: () => [] } as unknown as ParentNode & Element
  reader.sync(replaced, true)
  assert.equal(observers[0].disconnected, true)
  assert.equal(observers.length, 2)
})

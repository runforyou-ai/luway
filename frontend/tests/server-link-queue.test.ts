/** 验证连接链接携带的部署地址的暂存与交接。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import {
  clearPendingServerLink,
  hasPendingServerLink,
  offerServerLink,
  registerServerLinkReceiver,
} from "../src/lib/server-link-queue.ts"

test("待处理的连接链接交给每次登记的连接页，连接或取消后清除", () => {
  offerServerLink("https://first.example.com")
  const received: string[] = []
  // 重复挂载的连接页都能收到待处理地址。
  registerServerLinkReceiver((serverURL) => received.push(`a:${serverURL}`))()
  const unregister = registerServerLinkReceiver((serverURL) => received.push(`b:${serverURL}`))
  offerServerLink("https://second.example.com")
  assert.deepEqual(received, ["a:https://first.example.com", "b:https://first.example.com", "b:https://second.example.com"])
  assert.equal(hasPendingServerLink(), true)

  clearPendingServerLink()
  unregister()
  assert.equal(hasPendingServerLink(), false)
  registerServerLinkReceiver((serverURL) => received.push(`c:${serverURL}`))()
  assert.equal(received.length, 3)
})

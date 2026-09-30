/** 验证通知点击后待打开页面的暂存与交接。 */
import assert from "node:assert/strict"
import { test } from "node:test"
import {
  openNotificationPath,
  registerNotificationNavigator,
} from "../src/lib/notification-open-queue.ts"

test("未登记跳转处理时暂存最近一次页面，登记后立即交出", () => {
  openNotificationPath("/w/first/chats?conversation=1")
  openNotificationPath("/w/second/inbox?conversation=2")
  const opened: string[] = []
  const unregister = registerNotificationNavigator((path) => opened.push(path))
  assert.deepEqual(opened, ["/w/second/inbox?conversation=2"])
  openNotificationPath("/w/third/chats?conversation=3")
  openNotificationPath("")
  assert.deepEqual(opened, ["/w/second/inbox?conversation=2", "/w/third/chats?conversation=3"])
  unregister()
})

test("重新挂载时旧登记的取消不影响新登记，卸载期间到达的页面交给新登记", () => {
  const first: string[] = []
  const second: string[] = []
  const unregisterFirst = registerNotificationNavigator((path) => first.push(path))
  unregisterFirst()
  openNotificationPath("/w/team/chats?conversation=4")
  const unregisterSecond = registerNotificationNavigator((path) => second.push(path))
  unregisterFirst()
  openNotificationPath("/w/team/chats?conversation=5")
  assert.deepEqual(first, [])
  assert.deepEqual(second, ["/w/team/chats?conversation=4", "/w/team/chats?conversation=5"])
  unregisterSecond()
})

/** 验证登录前后的页面交接、登录过期提示与工作区最近页面记录。 */
import assert from "node:assert/strict"
import { beforeEach, test } from "node:test"

/** 构造一份内存存储。 */
function memoryStorage() {
  const values = new Map<string, string>()
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => void values.set(key, value),
    removeItem: (key: string) => void values.delete(key),
  }
}

const fakeWindow = { location: { hash: "" }, sessionStorage: memoryStorage(), localStorage: memoryStorage() }
Object.assign(globalThis, { window: fakeWindow })

const { clearSessionExpiredNotice, markSessionExpired, noteSessionEstablished, rememberLoginReturn, sessionExpiredNoticePending, takePendingReturnPath } = await import("../src/lib/login-return.ts")
const { lastWorkspacePath, rememberWorkspacePath } = await import("../src/lib/workspace-route.ts")

beforeEach(() => {
  fakeWindow.sessionStorage = memoryStorage()
  fakeWindow.localStorage = memoryStorage()
})

test("登录过期时记住所在的工作区页面并提示一次", () => {
  fakeWindow.location.hash = "#/w/acme/chats?conversation=c1"
  rememberLoginReturn(true)
  assert.equal(sessionExpiredNoticePending(), true)
  clearSessionExpiredNotice()
  assert.equal(sessionExpiredNoticePending(), false)
  assert.equal(takePendingReturnPath(), "/w/acme/chats?conversation=c1")
  assert.equal(takePendingReturnPath(), null)
})

test("未登录打开工作区页面只记住页面，不提示过期；不在工作区页面时不记住页面", () => {
  fakeWindow.location.hash = "#/w/acme/contacts"
  rememberLoginReturn(false)
  assert.equal(sessionExpiredNoticePending(), false)
  assert.equal(takePendingReturnPath(), "/w/acme/contacts")
  fakeWindow.location.hash = "#/workspaces"
  rememberLoginReturn(false)
  assert.equal(takePendingReturnPath(), null)
})

test("本页建立过会话后失效时提示过期，与所在页面无关，提示后回到未建立会话的状态", () => {
  fakeWindow.location.hash = "#/workspaces"
  noteSessionEstablished()
  rememberLoginReturn(false)
  assert.equal(sessionExpiredNoticePending(), true)
  clearSessionExpiredNotice()
  rememberLoginReturn(false)
  assert.equal(sessionExpiredNoticePending(), false)
  markSessionExpired()
  assert.equal(sessionExpiredNoticePending(), true)
})

test("按工作区分别记住最近停留的页面", () => {
  rememberWorkspacePath("acme", "/chats?conversation=c1")
  rememberWorkspacePath("beta", "/contacts")
  assert.equal(lastWorkspacePath("acme"), "/chats?conversation=c1")
  assert.equal(lastWorkspacePath("beta"), "/contacts")
  assert.equal(lastWorkspacePath("gamma"), null)
})

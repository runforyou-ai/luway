/** 用真实 React 生命周期验证图片预上传的替换、重试和释放。 */
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { stripTypeScriptTypes } from "node:module"
import { test, type TestContext } from "node:test"
import { runInNewContext } from "node:vm"
import * as React from "react"
import { createRoot } from "react-dom/client"
import { JSDOM } from "jsdom"

const source = stripTypeScriptTypes(readFileSync(new URL("../src/hooks/use-pending-image-upload.ts", import.meta.url), "utf8"))
  .replace(/import\s*\{([^}]+)\}\s*from "react"/, "const {$1} = React")
  .replace(/import\s*\{([^}]+)\}\s*from "@\/api"/, "const {$1} = api")
  .replace("export function usePendingImageUpload", "function usePendingImageUpload") + "\nexports.usePendingImageUpload = usePendingImageUpload"

/** 挂载共享 Hook，使用可控制完成顺序的上传请求。 */
async function host(t: TestContext) {
  const dom = new JSDOM("<div id='root'></div>")
  const previousWindow = Object.getOwnPropertyDescriptor(globalThis, "window")
  const previousDocument = Object.getOwnPropertyDescriptor(globalThis, "document")
  const previousAct = Object.getOwnPropertyDescriptor(globalThis, "IS_REACT_ACT_ENVIRONMENT")
  Object.defineProperties(globalThis, {
    window: { configurable: true, value: dom.window },
    document: { configurable: true, value: dom.window.document },
    IS_REACT_ACT_ENVIRONMENT: { configurable: true, value: true },
  })
  const uploads: { file: File; purpose: string; resolve: (file: { id: string }) => void; reject: (error: unknown) => void }[] = []
  const revoked: string[] = []
  const errors: unknown[] = []
  const exports: Record<string, any> = {}
  let sequence = 0
  runInNewContext(source, {
    React, exports,
    URL: {
      createObjectURL: () => `blob:preview-${++sequence}`,
      revokeObjectURL: (url: string) => revoked.push(url),
    },
    api: {
      uploadFile: (file: File, purpose: string) => new Promise((resolve, reject) => uploads.push({ file, purpose, resolve, reject })),
    },
  })
  let value: ReturnType<typeof exports.usePendingImageUpload>
  /** 通过实际渲染取得 Hook 的最新状态。 */
  function Harness() {
    value = exports.usePendingImageUpload({ purpose: "avatar", onError: (error: unknown) => errors.push(error) })
    return null
  }
  const root = createRoot(dom.window.document.getElementById("root")!)
  let unmounted = false
  /** 卸载后等待 React 执行预览释放。 */
  async function unmount() {
    if (unmounted) return
    unmounted = true
    await React.act(() => root.unmount())
  }
  t.after(async () => {
    await unmount()
    dom.window.close()
    for (const [key, descriptor] of [["window", previousWindow], ["document", previousDocument], ["IS_REACT_ACT_ENVIRONMENT", previousAct]] as const) {
      if (descriptor) Object.defineProperty(globalThis, key, descriptor)
      else Reflect.deleteProperty(globalThis, key)
    }
  })
  await React.act(() => root.render(React.createElement(React.StrictMode, null, React.createElement(Harness))))
  return { get value() { return value }, uploads, revoked, errors, unmount }
}

test("选择立即上传，保存等待同一请求且复用完成的文件编号", async (t) => {
  const h = await host(t)
  const file = new File(["image"], "avatar.png", { type: "image/png" })
  await React.act(() => h.value.select(file))
  assert.equal(h.uploads.length, 1)
  assert.equal(h.uploads[0].file, file)
  assert.equal(h.uploads[0].purpose, "avatar")
  const waiting = [h.value.ensureUploaded(), h.value.ensureUploaded()]
  assert.equal(h.uploads.length, 1)
  await React.act(async () => { h.uploads[0].resolve({ id: "uploaded" }) })
  assert.deepEqual(await Promise.all(waiting), ["uploaded", "uploaded"])
  assert.equal(h.value.pending.status, "uploaded")
  assert.equal(await h.value.ensureUploaded(), "uploaded")
  assert.equal(h.uploads.length, 1)
  await React.act(() => h.value.clear())
  assert.equal(h.value.pending, null)
  assert.deepEqual(h.revoked, ["blob:preview-1"])
})

test("替换图片后忽略旧结果，取消后不恢复预览或显示旧错误", async (t) => {
  const h = await host(t)
  await React.act(() => h.value.select(new File(["a"], "a.png")))
  await React.act(() => h.value.select(new File(["b"], "b.png")))
  await React.act(() => h.uploads[0].resolve({ id: "old" }))
  assert.equal(h.value.pending.file.name, "b.png")
  assert.equal(h.value.pending.fileID, "")
  await React.act(() => h.value.clear())
  await React.act(() => h.uploads[1].reject(new Error("cancelled image")))
  assert.equal(h.value.pending, null)
  assert.deepEqual(h.errors, [])
  assert.deepEqual(h.revoked, ["blob:preview-1", "blob:preview-2"])
})

test("失败后保存会重试当前文件，保留同一预览", async (t) => {
  const h = await host(t)
  await React.act(() => h.value.select(new File(["a"], "a.png")))
  const failure = new Error("upload failed")
  let saving: Promise<string | null>
  await React.act(() => { saving = h.value.ensureUploaded() })
  await React.act(() => h.uploads[0].reject(failure))
  assert.equal(await saving!, null)
  assert.equal(h.value.pending.status, "failed")
  assert.deepEqual(h.errors, [failure])
  let retry: Promise<string | null>
  await React.act(() => { retry = h.value.ensureUploaded() })
  assert.equal(h.uploads.length, 2)
  assert.equal(h.value.pending.previewURL, "blob:preview-1")
  await React.act(async () => { h.uploads[1].resolve({ id: "retry" }); await retry })
  assert.equal(h.value.pending.fileID, "retry")
})

test("卸载释放预览并忽略未完成上传的失败", async (t) => {
  const h = await host(t)
  await React.act(() => h.value.select(new File(["a"], "a.png")))
  await h.unmount()
  await React.act(() => h.uploads[0].reject(new Error("late failure")))
  assert.deepEqual(h.revoked, ["blob:preview-1"])
  assert.deepEqual(h.errors, [])
})

test("旧头像保存完成只清除对应上传，不清除新选择的头像", async (t) => {
  const h = await host(t)
  await React.act(() => h.value.select(new File(["a"], "a.png")))
  await React.act(() => h.uploads[0].resolve({ id: "old" }))
  await React.act(() => h.value.select(new File(["b"], "b.png")))
  await React.act(() => h.value.clear("old"))
  assert.equal(h.value.pending.file.name, "b.png")
  await React.act(() => h.uploads[1].resolve({ id: "new" }))
  await React.act(() => h.value.clear("new"))
  assert.equal(h.value.pending, null)
})

test("不含头像的旧保存完成时保留刚开始上传的新头像", async (t) => {
  const h = await host(t)
  await React.act(() => h.value.select(new File(["a"], "a.png")))
  await React.act(() => h.value.clear(""))
  assert.equal(h.value.pending.file.name, "a.png")
  await React.act(() => h.uploads[0].resolve({ id: "new" }))
  assert.equal(h.value.pending.fileID, "new")
})

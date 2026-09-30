/** 用真实表单和 React 生命周期验证自动保存快照与卸载隔离。 */
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { stripTypeScriptTypes } from "node:module"
import { test, type TestContext } from "node:test"
import { runInNewContext } from "node:vm"
import * as React from "react"
import { createRoot } from "react-dom/client"
import { useForm } from "react-hook-form"
import { JSDOM } from "jsdom"
import { z } from "zod"

/** 挂载真实保存 Hook，按指定顺序完成请求与防抖计时。 */
async function host(t: TestContext, autoSave = true) {
  const dom = new JSDOM("<div id='root'></div>")
  const globals = ["window", "document", "IS_REACT_ACT_ENVIRONMENT"] as const
  const previous = globals.map((key) => Object.getOwnPropertyDescriptor(globalThis, key))
  Object.defineProperties(globalThis, {
    window: { configurable: true, value: dom.window },
    document: { configurable: true, value: dom.window.document },
    IS_REACT_ACT_ENVIRONMENT: { configurable: true, value: true },
  })
  const timers = new Map<number, () => void>()
  let timerID = 0
  const requests: { values: { name: string }; resolve: (value: { name: string }) => void; reject: (error: Error) => void }[] = []
  const submitted: unknown[] = []
  const errors: unknown[] = []
  const navigations: unknown[] = []
  const modules: Record<string, any> = {
    react: React,
    "react-router": { useNavigate: () => (value: unknown) => navigations.push(value), useLocation: () => ({ pathname: "/form" }) },
    sonner: { toast: { error: (value: unknown) => errors.push(value) } },
    "@/api": { isApiError: () => false },
    "@/lib/form-errors": { requestErrorMessage: () => "api error" },
    "@/lib/session-navigation": { recoverSession: () => false },
    "@/contexts/unsaved-changes-context": { useUnsavedChangesContext: () => null },
  }
  for (const name of ["use-mounted-ref", "use-form-lifetime", "use-auto-save", "use-form-save"]) {
    const exports: Record<string, any> = {}
    const source = stripTypeScriptTypes(readFileSync(new URL(`../src/hooks/${name}.ts`, import.meta.url), "utf8"))
      .replace(/import\s*\{([^}]+)\}\s*from "([^"]+)"/g, 'const {$1} = modules["$2"]')
      .replace(/export function (\w+)/, "function $1")
    const functionName = name.replace(/-([a-z])/g, (_, letter) => letter.toUpperCase())
    runInNewContext(`${source}\nexports.${functionName} = ${functionName}`, {
      modules, exports, console: { warn() {} },
      setTimeout: (callback: () => void) => { timers.set(++timerID, callback); return timerID },
      clearTimeout: (id: number) => timers.delete(id),
    })
    modules[`@/hooks/${name}`] = exports
  }
  let form: ReturnType<typeof useForm<{ name: string }>>
  let save: ReturnType<typeof modules[string]["useFormSave"]>
  const schema = z.object({ name: z.string().trim().min(1) })
  /** 获取每次渲染后的表单和保存入口。 */
  function Harness() {
    form = useForm({ defaultValues: { name: "Initial" } })
    save = modules["@/hooks/use-form-save"].useFormSave({
      form, schema, autoSave,
      save: (values: { name: string }) => new Promise((resolve, reject) => requests.push({ values, resolve, reject })),
      savedValues: (values: { name: string }) => values,
      onSubmitted: (value: unknown) => submitted.push(value),
      errorMessage: "save failed", logLabel: "保存测试",
    })
    return null
  }
  const root = createRoot(dom.window.document.getElementById("root")!)
  let unmounted = false
  /** 卸载表单并刷新清理逻辑。 */
  async function unmount() {
    if (unmounted) return
    unmounted = true
    await React.act(() => root.unmount())
  }
  t.after(async () => {
    await unmount()
    dom.window.close()
    globals.forEach((key, index) => {
      if (previous[index]) Object.defineProperty(globalThis, key, previous[index]!)
      else Reflect.deleteProperty(globalThis, key)
    })
  })
  await React.act(() => root.render(React.createElement(React.StrictMode, null, React.createElement(Harness))))
  return {
    get form() { return form! }, get save() { return save }, requests, submitted, errors, navigations, unmount,
    async edit(name: string) { await React.act(() => form.setValue("name", name, { shouldDirty: true })) },
    async tick() {
      await React.act(() => { const batch = [...timers.values()]; timers.clear(); batch.forEach((callback) => callback()) })
    },
  }
}

test("旧保存完成保留请求期间的新输入，并串行提交最新值", async (t) => {
  const h = await host(t)
  await h.edit("First")
  await h.tick()
  await h.edit("Latest")
  await h.tick()
  assert.equal(h.requests.length, 1)
  await React.act(() => h.requests[0].resolve({ name: "First" }))
  assert.equal(h.form.getValues("name"), "Latest")
  assert.equal(h.requests.length, 2)
  assert.equal(h.requests[1].values.name, "Latest")
  await React.act(() => h.requests[1].resolve({ name: "Latest" }))
  await h.tick()
  assert.equal(h.requests.length, 2)
  assert.equal(h.form.formState.isDirty, false)
})

test("归一化结果成为保存基准，不触发重复写入", async (t) => {
  const h = await host(t)
  await h.edit(" normalized ")
  await h.tick()
  await React.act(() => h.requests[0].resolve({ name: "Normalized" }))
  assert.equal(h.form.getValues("name"), "Normalized")
  await h.tick()
  assert.equal(h.requests.length, 1)
})

test("回车提交与防抖保存共用队列，不发起并行写入", async (t) => {
  const h = await host(t)
  await h.edit("First")
  await h.tick()
  await h.edit("Latest")
  await React.act(() => h.save.submit(h.form.getValues()))
  assert.equal(h.requests.length, 1)
  await React.act(() => h.requests[0].resolve({ name: "First" }))
  assert.equal(h.requests[1].values.name, "Latest")
  await React.act(() => h.requests[1].resolve({ name: "Latest" }))
})

test("自动保存卸载时提交尚未发送的最新编辑", async (t) => {
  const h = await host(t)
  await h.edit("First")
  await h.tick()
  await h.edit("Latest")
  await h.unmount()
  await React.act(() => h.requests[0].resolve({ name: "First" }))
  assert.equal(h.requests[1].values.name, "Latest")
  await React.act(() => h.requests[1].resolve({ name: "Latest" }))
  assert.deepEqual(h.submitted, [])
})

test("关闭手动提交弹窗后，旧成功响应不触发关闭或跳转回调", async (t) => {
  const h = await host(t, false)
  let pending: Promise<boolean>
  await React.act(() => { pending = h.save.submit(h.form.getValues()) })
  await h.unmount()
  await React.act(async () => { h.requests[0].resolve({ name: "Saved" }); await pending })
  assert.deepEqual(h.submitted, [])
  assert.deepEqual(h.navigations, [])
})

test("关闭手动提交弹窗后忽略旧请求错误", async (t) => {
  const h = await host(t, false)
  let pending: Promise<boolean>
  await React.act(() => { pending = h.save.submit(h.form.getValues()) })
  await h.unmount()
  await React.act(async () => { h.requests[0].reject(new Error("late failure")); await pending })
  assert.deepEqual(h.errors, [])
})

test("详情回填同步保存基准，读取结果不会再次写回", async (t) => {
  const h = await host(t)
  await React.act(() => {
    h.form.reset({ name: "Fetched" })
    h.save.markSaved({ name: "Fetched" })
  })
  await h.tick()
  assert.equal(h.requests.length, 0)
  await h.edit("Edited")
  await h.tick()
  assert.equal(h.requests[0].values.name, "Edited")
  await React.act(() => h.requests[0].resolve({ name: "Edited" }))
})

test("确认弹窗提交与进行中的自动保存串行执行", async (t) => {
  const h = await host(t)
  await h.edit("First")
  await h.tick()
  await h.edit("Confirmed")
  let confirmed: Promise<boolean>
  await React.act(() => { confirmed = h.save.commit(h.form.getValues(), true) })
  assert.equal(h.requests.length, 1)
  await React.act(() => h.requests[0].resolve({ name: "First" }))
  assert.equal(h.requests.length, 2)
  assert.equal(h.requests[1].values.name, "Confirmed")
  assert.equal(h.form.getValues("name"), "Confirmed")
  await React.act(async () => { h.requests[1].resolve({ name: "Confirmed" }); await confirmed })
  await h.tick()
  assert.equal(h.requests.length, 2)
})

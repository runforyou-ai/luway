/** 验证来源导航的筛选保留、404 历史替换与未保存弹窗的关闭生命周期。 */
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { createRequire } from "node:module"
import { test, type TestContext } from "node:test"
import { runInNewContext } from "node:vm"
import * as React from "react"
import { createRoot } from "react-dom/client"
import * as Router from "react-router"
import { JSDOM } from "jsdom"
import { transformWithOxc } from "vite"

const require = createRequire(import.meta.url)

/** 创建真实 React 宿主并加载被测模块。 */
async function host(t: TestContext, mocks: Record<string, any> = {}) {
  const dom = new JSDOM("<div id='root'></div>")
  const globals = ["window", "document", "IS_REACT_ACT_ENVIRONMENT"] as const
  const previous = globals.map((key) => Object.getOwnPropertyDescriptor(globalThis, key))
  Object.defineProperties(globalThis, {
    window: { configurable: true, value: dom.window },
    document: { configurable: true, value: dom.window.document },
    IS_REACT_ACT_ENVIRONMENT: { configurable: true, value: true },
  })
  const root = createRoot(dom.window.document.getElementById("root")!)
  const warnings: any[][] = []
  t.after(async () => {
    await React.act(() => root.unmount())
    dom.window.close()
    globals.forEach((key, index) => {
      if (previous[index]) Object.defineProperty(globalThis, key, previous[index]!)
      else Reflect.deleteProperty(globalThis, key)
    })
  })
  return {
    root, warnings,
    async load(path: string, names: string[]) {
      const compiled = await transformWithOxc(readFileSync(new URL(`../src/${path}`, import.meta.url), "utf8"), path, { jsx: { runtime: "automatic" } })
      const code = compiled.code.replace(/import\s*\{([^}]+)\}\s*from\s*["']([^"']+)["'];?/g,
        (_, bindings: string, dependency: string) => `const {${bindings.replace(/\bas\b/g, ":")}} = require(${JSON.stringify(dependency)});`)
        .replace(/export (function|const) /g, "$1 ")
        .replace(/export\s*\{[^}]*\};?/g, "")
      const exports: Record<string, any> = {}
      runInNewContext(`${code}\n${names.map((name) => `exports.${name} = ${name}`).join(";\n")}`, {
        exports, URLSearchParams,
        require: (dependency: string) => mocks[dependency] ?? require(dependency),
        console: { warn: (...args: any[]) => warnings.push(args) },
      })
      return exports
    },
  }
}

test("新建团队的来源保留筛选并移除创建弹窗参数，普通链接保留原查询编码", async (t) => {
  const h = await host(t)
  const hooks = await h.load("hooks/use-return-to.ts", ["useReturnLink", "useReturnTo"])
  let filtered = "", original = ""
  /** 读取同一列表生成的两种来源链接。 */
  function Harness() {
    filtered = hooks.useReturnLink({ omitSearchParams: ["newTeam"] })("/contacts/teams/team-1")
    original = hooks.useReturnLink()("/contacts/teams/team-1?tab=members")
    return null
  }
  await React.act(() => h.root.render(React.createElement(Router.MemoryRouter, {
    initialEntries: ["/contacts/teams?search=研发%20团队&status=active&newTeam=1"],
  }, React.createElement(Harness))))
  const result = new URLSearchParams(filtered.split("?")[1])
  const source = new URL(result.get("returnTo")!, "https://example.test")
  assert.equal(source.pathname, "/contacts/teams")
  assert.equal(source.searchParams.get("search"), "研发 团队")
  assert.equal(source.searchParams.get("status"), "active")
  assert.equal(source.searchParams.has("newTeam"), false)
  assert.equal(new URLSearchParams(original.split("?")[1]).get("returnTo"), "/contacts/teams?search=研发%20团队&status=active&newTeam=1")
})

test("404 返回筛选列表并替换当前历史，日志保留实体编号", async (t) => {
  const h = await host(t)
  const hooks = await h.load("hooks/use-return-to.ts", ["useReturnTo"])
  /** 仅在详情路径触发记录不存在的导航。 */
  function Harness() {
    const location = Router.useLocation()
    hooks.useReturnTo("/channels", {
      notFound: location.pathname === "/channels/missing",
      logFields: { channel_id: "missing", channel_type: "website" },
    })
    return null
  }
  const router = Router.createMemoryRouter([{ path: "*", element: React.createElement(Harness) }], {
    initialEntries: ["/previous", "/channels/missing?returnTo=%2Fchannels%3Fstatus%3Ddisabled"], initialIndex: 1,
  })
  t.after(() => router.dispose())
  await React.act(() => h.root.render(React.createElement(Router.RouterProvider, { router })))
  assert.equal(router.state.location.pathname + router.state.location.search, "/channels?status=disabled")
  assert.equal(h.warnings.length, 1)
  assert.equal(h.warnings[0][1].channel_id, "missing")
  assert.equal(h.warnings[0][1].channel_type, "website")
  await React.act(() => router.navigate(-1))
  assert.equal(router.state.location.pathname, "/previous")
})

test("来源校验拒绝外部和其他列表地址，并接受显式允许的团队详情", async (t) => {
  const h = await host(t)
  const hooks = await h.load("hooks/use-return-to.ts", ["useReturnTo"])
  let returnTo = ""
  /** 读取经过校验的返回来源。 */
  function Harness() {
    returnTo = hooks.useReturnTo("/ai-employees", { allowed: (path: string) => /^\/contacts\/teams\/[^/]+$/.test(path) }).returnTo
    return null
  }
  for (const [source, expected] of [
    ["https://example.test/ai-employees", "/ai-employees"],
    ["//example.test/ai-employees", "/ai-employees"],
    ["/channels?status=disabled", "/ai-employees"],
    ["/contacts/teams/team-1?tab=members", "/contacts/teams/team-1?tab=members"],
  ]) {
    await React.act(() => h.root.render(React.createElement(Router.MemoryRouter, {
      key: source, initialEntries: [`/ai-employees/agent-1?returnTo=${encodeURIComponent(source)}`],
    }, React.createElement(Harness))))
    assert.equal(returnTo, expected)
  }
})

test("未保存确认随父弹窗关闭而清除，重新打开后可继续编辑或确认放弃", async (t) => {
  let dialog: any, confirmation: any, lifetime: any
  const mocks: Record<string, any> = {
    "react-i18next": { useTranslation: () => ({ t: (key: string) => key }) },
    "@/components/ui/dialog": { Dialog: (props: any) => { dialog = props; return props.open ? props.children : null } },
    "@/components/confirmation-dialog": { ConfirmationDialog: (props: any) => { confirmation = props; return null } },
  }
  const h = await host(t, mocks)
  mocks["@/contexts/unsaved-changes-context"] = await h.load("contexts/unsaved-changes-context.ts", ["UnsavedChangesContext", "useUnsavedChangesContext"])
  const { useFormLifetime } = await h.load("hooks/use-form-lifetime.ts", ["useFormLifetime"])
  const { UnsavedDialog } = await h.load("components/unsaved-dialog.tsx", ["UnsavedDialog"])
  let setOpen: React.Dispatch<React.SetStateAction<boolean>>
  /** 登记有改动的真实表单生命周期。 */
  function Form() { lifetime = useFormLifetime(true); return null }
  /** 控制父弹窗的外部打开状态。 */
  function Harness() {
    const [open, update] = React.useState(true)
    setOpen = update
    return React.createElement(UnsavedDialog, { open, onOpenChange: update }, React.createElement(Form))
  }
  await React.act(() => h.root.render(React.createElement(Router.MemoryRouter, null, React.createElement(Harness))))
  await React.act(() => dialog.onOpenChange(false))
  assert.equal(dialog.open, true)
  assert.equal(confirmation.open, true)
  await React.act(() => setOpen(false))
  assert.equal(confirmation.open, false)
  await React.act(() => setOpen(true))
  assert.equal(confirmation.open, false)
  await React.act(() => dialog.onOpenChange(false))
  await React.act(() => confirmation.onOpenChange(false))
  assert.equal(dialog.open, true)
  assert.equal(lifetime.discarded.current, false)
  await React.act(() => dialog.onOpenChange(false))
  await React.act(() => confirmation.onConfirm())
  assert.equal(dialog.open, false)
  assert.equal(confirmation.open, false)
  assert.equal(lifetime.discarded.current, true)
})

for (const attachment of [false, true]) test(`移动端首发后线程与输入状态沿用，资料页返回继续沿用，附件=${attachment}`, async (t) => {
  let thread: any, instance = 0, setDraft: React.Dispatch<React.SetStateAction<string>>
  const conversation = { id: "saved-1", type: "direct", direct: { peerIdentityId: "peer-1", peerName: "测试同事" } }
  /** 保留线程的本地输入状态并记录挂载次数。 */
  function Thread(props: any) {
    const [id] = React.useState(() => ++instance)
    const [draft, update] = React.useState("")
    setDraft = update
    thread = { ...props, id, draft }
    return React.createElement("p", null, draft)
  }
  /** 渲染头部容器中的子节点。 */
  const Box = ({ children }: any) => children ?? null
  const mocks: Record<string, any> = {
    "@/api": {
      ConversationType: { ConversationTypeDirect: "direct" }, OrganizationIdentityType: {},
      isDirectInboxConversation: (value: any) => value.type === "direct", isAgentInboxConversation: () => false,
    },
    "react-i18next": { useTranslation: () => ({ t: (key: string) => key }) },
    "@/apps/mobile/mobile-individual-thread": { MobileIndividualThread: Thread },
    "@/apps/mobile/mobile-page": { MobilePageHeader: () => null },
    "@/apps/mobile/mobile-navigation": { useMobileNavigation: () => ({ chatsURL: "/chats" }), mobileSearchPath: () => "/search" },
    "@/components/ui/button": { Button: Box },
    "@/components/loading-indicator": { LoadingIndicator: Box },
    "@/components/ui/dropdown-menu": { DropdownMenu: Box, DropdownMenuContent: Box, DropdownMenuItem: Box, DropdownMenuTrigger: Box },
    "@/features/inbox/agent-run-status": { assistantUnavailableLabel: () => "" },
    "@/features/inbox/use-conversation-summary": { useConversationSummary: () => ({ data: undefined, loading: true }) },
    "@/features/inbox/use-account-disabled-reason": { useAccountDisabledReason: () => "" },
    "@/features/inbox/use-conversation-typing": { useConversationTypingLabel: () => "" },
    "@/features/inbox/use-conversation-archive": { useConversationArchive: () => ({}) },
    "@/features/inbox/conversation-list-menu": { useConversationListActions: () => ({}) },
    "@/features/inbox/use-first-chat-message": { useFirstChatMessage: () => ({
      sendDirect: async () => ({ conversation, message: { id: "message-1" } }), refreshStarted: () => {},
    }) },
  }
  const h = await host(t, mocks)
  mocks["@/hooks/use-mounted-ref"] = await h.load("hooks/use-mounted-ref.ts", ["useMountedRef"])
  const { MobileIndividualConversationPage } = await h.load("apps/mobile/mobile-individual-conversation-page.tsx", ["MobileIndividualConversationPage"])
  const router = Router.createMemoryRouter([{ path: "/chats/direct/:conversationID", element: React.createElement(MobileIndividualConversationPage), children: [{ path: "profile", element: React.createElement("p", null, "资料") }] }], {
    initialEntries: [{ pathname: "/chats/direct/draft-1", state: { draftPeer: { identityId: "peer-1", displayName: "测试同事" } } }],
  })
  t.after(() => router.dispose())
  await React.act(() => h.root.render(React.createElement(Router.RouterProvider, { router })))
  const originalInstance = thread.id
  await React.act(() => setDraft("发送期间继续输入"))
  await React.act(async () => {
    if (attachment) thread.onAttachmentConversationCreated(conversation)
    else await thread.sendIndividualMessage({ text: "测试首发" })
  })
  assert.equal(router.state.location.pathname, "/chats/direct/saved-1")
  assert.equal(thread.conversationID, "saved-1")
  assert.equal(thread.id, originalInstance)
  assert.equal(thread.draft, "发送期间继续输入")
  await React.act(() => router.navigate("/chats/direct/saved-1/profile", { state: { conversation } }))
  assert.equal(thread.id, originalInstance)
  assert.equal(thread.enabled, false)
  await React.act(() => router.navigate(-1))
  assert.equal(thread.id, originalInstance)
  assert.equal(thread.draft, "发送期间继续输入")
})

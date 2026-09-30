/** 验证真实图片加载错误能够刷新下载地址并重新挂载图片，移动端预览可下载原图。 */
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { createRequire } from "node:module"
import { test } from "node:test"
import { runInNewContext } from "node:vm"
import * as React from "react"
import { createRoot } from "react-dom/client"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import * as ReactQuery from "@tanstack/react-query"
import { transformWithOxc } from "vite"
import { JSDOM } from "jsdom"

const require = createRequire(import.meta.url)

for (const mobile of [false, true]) test(`图片地址成功但加载失败后可重试，移动预览=${mobile}`, async (t) => {
  const dom = new JSDOM("<div id='root'></div>")
  const globals = ["window", "document", "IS_REACT_ACT_ENVIRONMENT"] as const
  const previous = globals.map((key) => Object.getOwnPropertyDescriptor(globalThis, key))
  Object.defineProperties(globalThis, {
    window: { configurable: true, value: dom.window },
    document: { configurable: true, value: dom.window.document },
    IS_REACT_ACT_ENVIRONMENT: { configurable: true, value: true },
  })
  let reads = 0
  const opened: string[] = []
  /** 对话框仅在打开时渲染内容。 */
  const Dialog = ({ open, children }: any) => open ? children : null
  /** 保留图片与按钮的 DOM 行为。 */
  const Box = ({ children }: any) => React.createElement("div", null, children)
  /** 渲染原生按钮并丢弃样式属性。 */
  const Button = ({ variant: _variant, size: _size, ...props }: any) => React.createElement("button", props)
  const mocks: Record<string, any> = {
    "@tanstack/react-query": ReactQuery,
    "react-router": { useNavigate: () => () => {} },
    "react-i18next": { useTranslation: () => ({ t: (key: string) => key }) },
    "@/api": {
      MessageAttachmentTransferStatus: { MessageAttachmentTransferReady: "ready" },
      getAttachmentDownload: async () => { reads++; return { previewUrl: "https://files.example/image.png", url: "https://files.example/download.png" } },
    },
    "@/hooks/resource-keys": { resourceKeys: { attachmentDownload: (...key: string[]) => key } },
    "@/lib/file-size": { formatFileSize: () => "1 KB" },
    "@/lib/utils": { cn: (...values: unknown[]) => values.filter(Boolean).join(" ") },
    "@/lib/session-navigation": { recoverSession: () => false },
    "@/platform/app-platform": { resolveAppPlatform: () => mobile ? "mobile" : "web" },
    "@/platform/external-navigation": { openExternalURL: async (url: string) => { opened.push(url) } },
    "@/contexts/attachment-queue-context": { useAttachmentQueue: () => null, useAttachmentJob: () => undefined },
    "@/components/attachment-name": { AttachmentName: Box },
    "@/components/ui/button": { Button },
    "@/components/ui/dialog": { Dialog, DialogContent: Box, DialogHeader: Box, DialogTitle: Box },
  }
  /** 编译被测组件，保留真实 React、查询缓存和图片元素。 */
  async function load(path: string, name: string) {
    const source = readFileSync(new URL(`../src/${path}`, import.meta.url), "utf8")
    const compiled = await transformWithOxc(source, path, { jsx: { runtime: "automatic" } })
    const code = compiled.code.replace(/import\s*\{([^}]+)\}\s*from\s*["']([^"']+)["'];?/g,
      (_, bindings: string, dependency: string) => `const {${bindings.replace(/\bas\b/g, ":")}} = require(${JSON.stringify(dependency)});`)
      .replace(/export function /g, "function ")
      .replace(/export\s*\{[^}]*\};?/g, "")
    const exports: Record<string, any> = {}
    runInNewContext(`${code}\nexports.${name} = ${name}`, {
      exports, require: (dependency: string) => mocks[dependency] ?? require(dependency),
      window: dom.window, document: dom.window.document, console,
    })
    return exports
  }
  mocks["@/hooks/use-resource"] = await load("hooks/use-resource.ts", "useResource")
  mocks["./attachment-content"] = await load("features/inbox/attachment-content.tsx", "AttachmentContent")
  const { ConversationAttachment } = await load("features/inbox/conversation-attachment.tsx", "ConversationAttachment")
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  const root = createRoot(dom.window.document.getElementById("root")!)
  t.after(async () => {
    await React.act(() => root.unmount())
    client.clear()
    dom.window.close()
    globals.forEach((key, index) => {
      if (previous[index]) Object.defineProperty(globalThis, key, previous[index]!)
      else Reflect.deleteProperty(globalThis, key)
    })
  })
  await React.act(() => root.render(React.createElement(QueryClientProvider, { client }, React.createElement(ConversationAttachment, {
    attachment: { id: "file", name: "image.png", byteSize: 1024, imageWidth: 100, imageHeight: 100, transferStatus: "ready" },
    body: "", conversationID: "conversation", messageID: "message", originatedAt: "", timeLabel: "", timeTitle: "", incoming: true, bubbleClassName: "",
  }))))
  await React.act(() => new Promise((resolve) => setTimeout(resolve, 10)))
  assert.equal(reads, 1)
  let image = dom.window.document.querySelector("img")!
  if (mobile) {
    await React.act(() => dom.window.document.querySelector<HTMLButtonElement>('button[aria-label="attachmentPreview"]')!.click())
    image = [...dom.window.document.querySelectorAll("img")].at(-1)!
  }
  await React.act(() => image.dispatchEvent(new dom.window.Event("error")))
  const retry = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "attachmentPreviewRetry")!
  assert.ok(retry, "图片加载失败应展示重试")
  await React.act(() => retry.click())
  assert.equal(reads, 2, "重试应重新签发下载地址")
  const retried = [...dom.window.document.querySelectorAll("img")].at(-1)!
  assert.notEqual(retried, image, "地址相同时也应重新加载图片")
  assert.equal(retried.src, image.src)
  assert.ok(!dom.window.document.body.textContent?.includes("attachmentPreviewRetry"))
  if (mobile) {
    const download = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "attachmentDownload")!
    assert.ok(download, "移动端图片预览应提供下载")
    await React.act(() => download.click())
    await React.act(() => new Promise((resolve) => setTimeout(resolve, 10)))
    assert.deepEqual(opened, ["https://files.example/download.png"], "下载应打开签发的下载地址")
  }
})

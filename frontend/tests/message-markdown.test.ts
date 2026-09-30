/** 用实际发布给 Messenger 的共享组件验证正文和流式更新。 */
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { test } from "node:test"
import { JSDOM } from "jsdom"

const bundle = readFileSync(new URL("../../internal/publicweb/dist/markdown.js", import.meta.url), "utf8")

/** 创建只执行本地构建产物的 DOM 测试宿主。 */
function host(t: { after: (callback: () => void) => void }) {
  const dom = new JSDOM('<html lang="zh-CN"><body><div id="message"></div></body></html>', { runScripts: "outside-only", url: "https://app.example.test" })
  dom.window.eval(bundle)
  const api = dom.window.MessengerMarkdown
  const container = dom.window.document.getElementById("message")!
  t.after(() => { api.unmount(container); dom.window.close() })
  return { api, container, window: dom.window }
}

/** 等待 React 完成异步提交后检查实际 DOM。 */
async function rendered(check: () => void) {
  const deadline = Date.now() + 2000
  while (true) {
    try { check(); return } catch (error) {
      if (Date.now() >= deadline) throw error
      await new Promise((resolve) => setTimeout(resolve, 5))
    }
  }
}

test("共享正文支持中文、GFM 表格、任务列表和代码原文复制", async (t) => {
  const { api, container, window } = host(t)
  let copied = ""
  Object.defineProperty(window.navigator, "clipboard", { value: { writeText: async (value: string) => { copied = value } } })
  api.render(container, '# 标题\n\n**重点**\n第二行\n\n- [x] 完成\n- [ ] 待办\n\n| 名称 | 结果 |\n| --- | --- |\n| 测试 | 成功 |\n\n```js\nconst a = "<安全>";\n```', "agent")
  await rendered(() => {
    assert.equal(container.querySelector("h1")?.textContent, "标题")
    assert.equal(container.querySelector("strong")?.textContent, "重点")
    assert.equal(container.querySelectorAll('input[type="checkbox"]').length, 2)
    assert.equal(container.querySelector("td")?.textContent, "测试")
    assert.equal(container.querySelector("pre code")?.textContent, 'const a = "<安全>";\n')
  })
  container.querySelector("button")!.click()
  await rendered(() => assert.equal(copied, 'const a = "<安全>";\n'))
})

test("流式输入修复未闭合语法并保留已完成块，结束后与历史正文一致", async (t) => {
  const { api, container } = host(t)
  const prefix = "已经完成的段落。\n\n"
  api.render(container, prefix + "**正在", "agent", true)
  await rendered(() => assert.equal(container.querySelector("strong")?.textContent, "正在"))
  const firstParagraph = container.querySelector("p")
  const body = prefix + '**正在生成**\n\n```ts\nconst answer = 42;\n```\n\n| 项目 | 值 |\n| --- | --- |\n| 答案 | 42 |'
  for (let length = prefix.length + 5; length <= body.length; length += 7) {
    api.render(container, body.slice(0, length), "agent", true)
    await new Promise((resolve) => setTimeout(resolve, 5))
    assert.equal(container.querySelector("p"), firstParagraph)
  }
  api.render(container, body, "agent", false)
  await rendered(() => {
    assert.equal(container.querySelector("strong")?.textContent, "正在生成")
    assert.equal(container.querySelector("pre code")?.textContent, "const answer = 42;\n")
    assert.equal(container.querySelector("td")?.textContent, "答案")
  })
  assert.equal(container.querySelector("p"), firstParagraph)
  const history = container.ownerDocument.createElement("div")
  container.appendChild(history)
  api.render(history, body, "agent")
  await rendered(() => assert.equal(history.querySelector(".message-markdown")?.innerHTML, container.querySelector(".message-markdown")?.innerHTML))
})

test("正文不执行 HTML 或危险 URL，安全链接保留浏览器语义", async (t) => {
  const { api, container } = host(t)
  api.render(container, '<script>alert(1)</script>\n\n<img src="x" onerror="alert(1)">\n\n[危险](javascript:alert%281%29) [本地](file:///etc/passwd) [网页](https://example.com)\n\n```html\n<script>example</script>\n```', "agent")
  await rendered(() => assert.equal(container.querySelectorAll("a").length, 3))
  assert.equal(container.querySelector("script, [onerror], img"), null)
  assert.equal(container.querySelector('a[href^="javascript:"], a[href^="file:"]'), null)
  const link = container.querySelector('a[href="https://example.com"]')!
  assert.equal(link.getAttribute("target"), "_blank")
  assert.equal(link.getAttribute("rel"), "noopener noreferrer")
  assert.equal(container.querySelector("code")?.textContent, "<script>example</script>\n")
})

test("人工和访客纯文本保留星号与换行，卸载后可重新使用正文节点", async (t) => {
  const { api, container } = host(t)
  api.render(container, "**AI**", "agent")
  await rendered(() => assert.equal(container.querySelector("strong")?.textContent, "AI"))
  api.render(container, "**原样**\n@成员 <标签>", "user")
  assert.equal(container.textContent, "**原样**\n@成员 <标签>")
  assert.equal(container.childElementCount, 0)
  api.render(container, "# 访客原文", null)
  assert.equal(container.textContent, "# 访客原文")
  assert.equal(container.childElementCount, 0)
  api.unmount(container)
  api.render(container, "**新回复**", "agent")
  await rendered(() => assert.equal(container.querySelector("strong")?.textContent, "新回复"))
})

test("未完成链接在生成中不可跳转，脚注在多条消息之间保持独立", async (t) => {
  const { api, container } = host(t)
  api.render(container, "[阅读文档](https://exa", "agent", true)
  await rendered(() => assert.ok(container.textContent?.includes("阅读文档")))
  assert.equal(container.querySelector("a[href]"), null)
  const body = "脚注[^1]\n\n[^1]: 说明"
  api.render(container, body, "agent")
  const other = container.ownerDocument.createElement("div")
  container.append(other)
  api.render(other, body, "agent")
  await rendered(() => assert.equal(container.querySelectorAll('a[data-footnote-ref]').length, 2))
  const links = [...container.querySelectorAll('a[data-footnote-ref]')]
  assert.notEqual(links[0].getAttribute("href"), links[1].getAttribute("href"))
  for (const link of links) assert.ok(container.ownerDocument.getElementById(link.getAttribute("href")!.slice(1)))
})

/** 创建去掉模板指令的访客聊天页，外部脚本由测试自行执行。 */
function messengerPage(t: { after: (callback: () => void) => void }) {
  const template = readFileSync(new URL("../../internal/publicweb/page.html", import.meta.url), "utf8")
    .replace(/<style>[\s\S]*?<\/style>/g, "").replace(/\{\{[\s\S]*?\}\}/g, "")
  const dom = new JSDOM(template, { runScripts: "outside-only", url: "https://app.example.test/chat" })
  const { window } = dom
  const document = window.document
  document.documentElement.lang = "zh-CN"
  const messages = document.getElementById("cv-messages")!
  const resized: (() => void)[] = []
  window.MESSENGER_COMPOSER_EMOJIS = []
  window.ResizeObserver = class {
    constructor(callback: () => void) { resized.push(callback) }
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  t.after(() => { window.MessengerMarkdown?.unmount(messages); dom.window.close() })
  return { window, document, messages, resized, chatScript: readFileSync(new URL("../../internal/publicweb/chat.js", import.meta.url), "utf8") }
}

/** 访客目录接口返回的单个测试会话。 */
function messengerDirectory(preview: string, last: { messageSeq: string; originatedAt: string }) {
  return { visitorToken: "test", reception: { handlerType: null, handlerName: "", handlerAvatarUrl: "", online: false, reply: "soon", nextOpeningAt: null }, receptionRefreshAt: null, conversations: [{ id: "conversation", title: "测试", preview, lastMessageSeq: last.messageSeq, lastMessageAt: last.originatedAt }] }
}

test("网站引用与 Markdown 正文共存，定位历史后保持阅读位置", async (t) => {
  const { window, document, messages, resized, chatScript } = messengerPage(t)
  let located: Element | null = null
  Object.defineProperty(messages, "clientHeight", { value: 200 })
  Object.defineProperty(messages, "scrollHeight", { get: () => 1000 + messages.querySelectorAll("h1").length * 200 })
  window.HTMLElement.prototype.scrollIntoView = function () {
    assert.equal(this.querySelector("h1")?.textContent, "最早回复")
    located = this
    messages.scrollTop = 120
  }
  const old = { id: "old-ai", messageSeq: "9007199254740992", author: "agent", senderIdentityType: "agent", body: "# 最早回复", preview: "最早回复", originatedAt: "2026-09-07T00:00:00Z" }
  const replies = [
    { id: "reply-ai", messageSeq: "9007199254740993", author: "agent", senderIdentityType: "agent", body: "**回答**", preview: "回答", originatedAt: "2026-09-07T00:01:00Z", replyTo: { id: old.id, author: "agent", preview: old.preview, deleted: false } },
    { id: "reply-human", messageSeq: "9007199254740994", author: "agent", senderIdentityType: "user", body: "**人工正文**", preview: "**人工正文**", originatedAt: "2026-09-07T00:02:00Z", replyTo: { id: "human", author: "agent", preview: "**人工原文**", deleted: false } },
  ]
  window.fetch = async (path: string) => ({ ok: true, json: async () => {
    if (path.endsWith("/messenger")) return messengerDirectory("**人工正文**", replies[1])
    if (path.includes("?before=")) return { messages: [old], before: "", after: "" }
    if (path.includes("?after=")) return { messages: [], before: "", after: "latest" }
    return { messages: replies, before: "earlier", after: "latest" }
  } })
  window.eval(bundle)
  window.eval(chatScript)
  await rendered(() => assert.equal(document.getElementById("cv-home-recent")!.hidden, false))
  document.getElementById("cv-home-recent")!.click()
  await rendered(() => {
    assert.equal(document.querySelector('[data-message-id="reply-ai"] .message-markdown strong')?.textContent, "回答")
    assert.equal(document.querySelector('[data-message-id="reply-ai"] .cv-message-reference-body')?.textContent, "最早回复")
    assert.equal(document.querySelector('[data-message-id="reply-human"] .cv-message-reference-body')?.textContent, "**人工原文**")
    assert.equal(document.querySelector('[data-message-id="reply-human"] .message-markdown'), null)
  })
  document.querySelector<HTMLButtonElement>('[data-message-id="reply-ai"] .cv-message-reply')!.click()
  assert.equal(document.getElementById("cv-composer-reference-body")!.textContent, "回答")
  document.querySelector<HTMLButtonElement>('[data-message-id="reply-ai"] .cv-message-reference')!.click()
  await rendered(() => {
    assert.equal(located?.getAttribute("data-message-id"), "old-ai")
    assert.equal(document.querySelector('[data-message-id="old-ai"] h1')?.textContent, "最早回复")
    assert.equal(messages.scrollTop, 120)
  })
  resized.forEach((callback) => callback())
  assert.equal(messages.scrollTop, 120)
  document.getElementById("cv-latest-message")!.click()
  assert.equal(messages.scrollTop, messages.scrollHeight)
})

test("Markdown 脚本晚于聊天脚本到达时，AI 正文先显示原文，载入后升级为 Markdown", async (t) => {
  const { window, document, chatScript } = messengerPage(t)
  const replies = [
    { id: "reply-ai", messageSeq: "1", author: "agent", senderIdentityType: "agent", body: "**回答**", preview: "回答", originatedAt: "2026-09-07T00:01:00Z", replyTo: null },
    { id: "reply-human", messageSeq: "2", author: "agent", senderIdentityType: "user", body: "**人工正文**", preview: "**人工正文**", originatedAt: "2026-09-07T00:02:00Z", replyTo: null },
  ]
  window.fetch = async (path: string) => ({ ok: true, json: async () => {
    if (path.endsWith("/messenger")) return messengerDirectory("**人工正文**", replies[1])
    return { messages: replies, before: "", after: "" }
  } })
  window.eval(chatScript)
  await rendered(() => assert.equal(document.getElementById("cv-home-recent")!.hidden, false))
  document.getElementById("cv-home-recent")!.click()
  await rendered(() => assert.ok(document.querySelector('[data-message-id="reply-ai"]')))
  assert.equal(document.querySelector(".message-markdown"), null)
  assert.ok(document.querySelector('[data-message-id="reply-ai"]')!.textContent!.includes("**回答**"))
  window.eval(bundle)
  document.getElementById("cv-markdown-script")!.dispatchEvent(new window.Event("load"))
  await rendered(() => assert.equal(document.querySelector('[data-message-id="reply-ai"] .message-markdown strong')?.textContent, "回答"))
  assert.equal(document.querySelector('[data-message-id="reply-human"] .message-markdown'), null)
  assert.ok(document.querySelector('[data-message-id="reply-human"]')!.textContent!.includes("**人工正文**"))
})

test("Markdown 迟到升级时，未跟随底部的阅读位置以视口内消息为锚点保持不动", async (t) => {
  const { window, document, messages, chatScript } = messengerPage(t)
  const replies = ["a1", "a2", "a3"].map((id, index) => ({ id, messageSeq: String(index + 1), author: "agent", senderIdentityType: "agent", body: `**${id}**`, preview: id, originatedAt: "2026-09-07T00:01:00Z", replyTo: null }))
  window.fetch = async (path: string) => ({ ok: true, json: async () => {
    if (path.endsWith("/messenger")) return messengerDirectory("a3", replies[2])
    return { messages: replies, before: "", after: "" }
  } })
  // 消息原文高 200，渲染为 Markdown 后高 400；消息列表视口高 200。
  const height = (node: Element) => node.hasAttribute("data-message-id") ? (node.querySelector(".message-markdown") ? 400 : 200) : 0
  let scrollTop = 0
  Object.defineProperty(messages, "scrollTop", { get: () => scrollTop, set: (value: number) => { scrollTop = value } })
  Object.defineProperty(messages, "clientHeight", { value: 200 })
  Object.defineProperty(messages, "scrollHeight", { get: () => Array.from(messages.children).reduce((total, node) => total + height(node), 0) })
  window.HTMLElement.prototype.getBoundingClientRect = function (this: HTMLElement) {
    if (this === messages) return { top: 0, bottom: 200 } as DOMRect
    const siblings = Array.from(messages.children)
    const top = siblings.slice(0, siblings.indexOf(this)).reduce((total, node) => total + height(node), 0) - scrollTop
    return { top, bottom: top + height(this) } as DOMRect
  }
  window.eval(chatScript)
  await rendered(() => assert.equal(document.getElementById("cv-home-recent")!.hidden, false))
  document.getElementById("cv-home-recent")!.click()
  await rendered(() => assert.ok(document.querySelector('[data-message-id="a3"]')))
  // 焦点停在已滚出视口的 a1，视口顶部是 a2。
  document.querySelector<HTMLElement>('[data-message-id="a1"]')!.focus()
  scrollTop = 250
  messages.dispatchEvent(new window.Event("scroll"))
  messages.dispatchEvent(new window.Event("scroll"))
  const anchor = document.querySelector('[data-message-id="a2"]')!
  const anchorTop = anchor.getBoundingClientRect().top
  window.eval(bundle)
  document.getElementById("cv-markdown-script")!.dispatchEvent(new window.Event("load"))
  assert.ok(anchor.querySelector(".message-markdown"))
  assert.equal(anchor.getBoundingClientRect().top, anchorTop)
})

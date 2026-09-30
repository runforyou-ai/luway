/** 在成员聊天和网站聊天窗口 中统一渲染完整或生成中的 Markdown。 */
import { createContext, createElement, memo, useContext, useId, useMemo, useRef, useState, type ComponentProps, type ReactNode } from "react"
import type { Element } from "hast"
import { Streamdown, defaultRehypePlugins, type Components, type ExtraProps } from "streamdown"
import chineseCopy from "../i18n/locales/zh-CN/markdown"
import englishCopy from "../i18n/locales/en-US/markdown"
import { highlightMentions } from "@/lib/mention-highlight"
import "./message-markdown.css"

type MessageMarkdownProps = {
  children: string
  streaming?: boolean
  locale?: string
  mentions?: string[]
  onOpenLink?: (url: string) => void | Promise<void>
  renderCodeBlock?: (language: string, code: string) => ReactNode | undefined
}


const MarkdownContext = createContext<{ copy: typeof englishCopy; onOpenLink?: MessageMarkdownProps["onOpenLink"]; renderCodeBlock?: MessageMarkdownProps["renderCodeBlock"] }>({ copy: chineseCopy })
// 禁用原始 HTML 解析，保留标签和 URL 的清理规则。
const rehypePlugins = [defaultRehypePlugins.sanitize]

/** 按宿主平台打开正文链接并保留浏览器的链接语义。 */
function MarkdownLink({ node, href, children, ...props }: ComponentProps<"a"> & ExtraProps) {
  const { onOpenLink, copy } = useContext(MarkdownContext)
  const [failed, setFailed] = useState(false)
  return <>
    <a {...props} href={href || undefined} target={href?.startsWith("#") ? undefined : "_blank"} rel="noopener noreferrer" onClick={onOpenLink && href && !href.startsWith("#") ? (event) => {
      event.preventDefault()
      setFailed(false)
      Promise.resolve().then(() => onOpenLink(href)).catch((error) => {
        console.warn("打开消息链接失败", error)
        setFailed(true)
      })
    } : undefined}>{children}</a>
    {failed ? <span role="status">{copy.linkFailed}</span> : null}
  </>
}

/** 按语言交给宿主渲染代码块，宿主未处理时展示代码原文并复制当前已经生成的内容。 */
function MarkdownCodeBlock({ node, children }: ComponentProps<"pre"> & ExtraProps) {
  const { copy, renderCodeBlock } = useContext(MarkdownContext)
  const pre = useRef<HTMLPreElement>(null)
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">("idle")
  // 从语法树读取代码块语言与原文。
  const code = node?.children.find((child): child is Element => child.type === "element" && child.tagName === "code")
  const classNames = Array.isArray(code?.properties.className) ? code.properties.className.map(String) : []
  const language = classNames.find((name) => name.startsWith("language-"))?.slice("language-".length) ?? ""
  const custom = code && renderCodeBlock
    ? renderCodeBlock(language, code.children.map((child) => (child.type === "text" ? child.value : "")).join(""))
    : undefined
  if (custom !== undefined) return custom
  return <div className="message-markdown-code">
    <div className="message-markdown-code-actions">
      <span role="status">{copyState === "copied" ? copy.copied : copyState === "failed" ? copy.copyFailed : ""}</span>
      <button type="button" onClick={async () => {
        try {
          await navigator.clipboard.writeText(pre.current?.textContent ?? "")
          setCopyState("copied")
        } catch (error) {
          console.warn("复制消息代码失败", error)
          setCopyState("failed")
        }
      }}>{copy.copyCode}</button>
    </div>
    <pre ref={pre} tabIndex={0}>{children}</pre>
  </div>
}

// 链接协议限定为网页、邮件和消息内锚点。
const urlTransform = (url: string) => /^(https?:\/\/|mailto:|#)/i.test(url) ? url : ""

// 管理端和访客页面共用 Markdown 元素样式。
const components: Components = {
  ...Object.fromEntries(["p", "h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "li", "blockquote", "hr", "strong", "em", "del", "thead", "tbody", "tr", "th", "td", "input"].map((tag) => [tag, ({ node, ...props }: { node?: unknown }) => createElement(tag, props)])),
  a: MarkdownLink,
  pre: MarkdownCodeBlock,
  code: ({ node, ...props }) => <code {...props} />,
  table: ({ node, ...props }) => <div className="message-markdown-table" tabIndex={0}><table {...props} /></div>,
  img: ({ node, ...props }) => <img {...props} loading="lazy" />,
}

/** 保持正文分块结构，仅在生成中修复未闭合语法，结束时保留已有 DOM。 */
export const MessageMarkdown = memo(function MessageMarkdown({ children, streaming = false, locale = "zh-CN", mentions, onOpenLink, renderCodeBlock }: MessageMarkdownProps) {
  const id = useId()
  const context = useMemo(() => ({ copy: locale.startsWith("zh") ? chineseCopy : englishCopy, onOpenLink, renderCodeBlock }), [locale, onOpenLink, renderCodeBlock])
  const remarkRehypeOptions = useMemo(() => ({ clobberPrefix: `message-${id}-` }), [id])
  const plugins = useMemo(
    () => (mentions?.length ? [...rehypePlugins, highlightMentions(mentions)] : rehypePlugins),
    [mentions],
  )
  return <MarkdownContext.Provider value={context}>
    <Streamdown className="message-markdown" mode="streaming"
      isAnimating={streaming} parseIncompleteMarkdown={streaming} skipHtml
      rehypePlugins={plugins} components={components}
      urlTransform={urlTransform}
      remarkRehypeOptions={remarkRehypeOptions}
      controls={false}>
      {children}
    </Streamdown>
  </MarkdownContext.Provider>
})

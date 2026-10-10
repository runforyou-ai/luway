/** 网站渠道的编辑页定义：聊天窗口与使用页签、与 URL 同步的子页签和聊天窗口实时预览。 */
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react"
import { useSearchParams } from "react-router"

import type {
  ChannelType,
  MessageChannelDetail,
  WebsiteChannel,
  WebsiteChannelChatInterfaceInput,
  WebsiteChannelHomeInput,
} from "@/api"
import type { ChannelEditDefinition, ChannelEditTab } from "@/features/channels/channel-edit-definition"
import {
  WebsiteChannelChatInterfacePanel,
  type WebsiteChatInterfaceSection,
} from "@/features/channels/website/website-channel-chat-interface-panel"
import {
  WebsiteChannelUsagePanel,
  type WebsiteChannelAccessTab,
} from "@/features/channels/website/website-channel-usage-panel"
import {
  WebsiteChatPreview,
  type WebsiteHelpCenterPreviewDraft,
  type WebsiteMessengerPreviewValue,
} from "@/features/channels/website/website-chat-preview"

type WebsiteChannelDetail = MessageChannelDetail<typeof ChannelType.Website>

type PreviewDraftChange =
  | WebsiteChannelChatInterfaceInput
  | WebsiteChannelHomeInput
  | WebsiteHelpCenterPreviewDraft

const chatInterfaceSections = ["appearance", "conversation", "home", "help-center"] as const satisfies readonly WebsiteChatInterfaceSection[]
const accessTabs = ["embed", "link"] as const satisfies readonly WebsiteChannelAccessTab[]

/** 聊天窗口预览草稿及其合并入口。 */
const WebsitePreviewContext = createContext<{
  value: WebsiteMessengerPreviewValue
  merge: (value: PreviewDraftChange) => void
} | null>(null)

/** 读取网站渠道编辑页的预览草稿。 */
function useWebsitePreview() {
  const context = useContext(WebsitePreviewContext)
  if (!context) throw new Error("缺少网站渠道预览上下文")
  return context
}

/** 把已保存的聊天窗口、首页与帮助中心设置归一化为实时预览值。 */
function savedPreviewValue(channel: WebsiteChannel): WebsiteMessengerPreviewValue {
  return {
    title: channel.chatInterface.title,
    greetingMessage: channel.chatInterface.greetingMessage ?? "",
    themeColor: channel.chatInterface.themeColor,
    attachmentsEnabled: channel.chatInterface.attachmentsEnabled,
    emojiEnabled: channel.chatInterface.emojiEnabled,
    ratingEnabled: channel.chatInterface.ratingEnabled,
    multipleConversationsEnabled: channel.chatInterface.multipleConversationsEnabled,
    enabled: channel.home.enabled,
    welcome: channel.home.welcome,
    headline: channel.home.headline,
    blocks: channel.home.blocks,
    links: channel.home.links,
    channelId: channel.id,
    helpEnabled: channel.helpCenter.enabled,
    helpKnowledgeBaseIds: channel.helpCenter.knowledgeBaseIds,
  }
}

/** 承载聊天窗口预览草稿：聊天窗口、首页与帮助中心三份表单上报合并，初始值取已保存设置。 */
function WebsitePreviewScope({ channel, children }: { channel: WebsiteChannelDetail; children: ReactNode }) {
  const [draft, setDraft] = useState(() => savedPreviewValue(channel))
  const merge = useCallback((value: PreviewDraftChange) => setDraft((current) => ({ ...current, ...value })), [])
  // 发布的知识库取渠道详情中的已保存值，详情刷新后随之更新。
  const savedKnowledgeBaseIds = channel.helpCenter.knowledgeBaseIds
  const context = useMemo(
    () => ({ value: { ...draft, helpKnowledgeBaseIds: savedKnowledgeBaseIds }, merge }),
    [draft, merge, savedKnowledgeBaseIds],
  )
  return <WebsitePreviewContext.Provider value={context}>{children}</WebsitePreviewContext.Provider>
}

/** 读取页签内与 URL 同步的子页签参数，所在页签打开且参数无效时改写为默认值。 */
function useTabSearchParam<T extends string>(tab: ChannelEditTab, name: string, values: readonly T[]) {
  const [searchParams, setSearchParams] = useSearchParams()
  const requested = searchParams.get(name)
  const value = values.find((item) => item === requested) ?? values[0]
  const active = searchParams.get("tab") === tab

  useEffect(() => {
    if (!active || requested === value) return
    const nextParams = new URLSearchParams(searchParams)
    nextParams.set(name, value)
    setSearchParams(nextParams, { replace: true })
  }, [active, name, requested, searchParams, setSearchParams, value])

  /** 切换子页签并同步 URL。 */
  function setValue(next: T) {
    const nextParams = new URLSearchParams(searchParams)
    nextParams.set("tab", tab)
    nextParams.set(name, next)
    setSearchParams(nextParams, { replace: true })
  }

  return [value, setValue] as const
}

/** 聊天窗口页签，子页签与 URL 的 section 参数同步。 */
function WebsiteChatInterfaceTab({ channel, onUpdated }: { channel: WebsiteChannelDetail; onUpdated: () => void }) {
  const [section, setSection] = useTabSearchParam("chat-interface", "section", chatInterfaceSections)
  const { merge } = useWebsitePreview()
  return (
    <WebsiteChannelChatInterfacePanel
      channel={channel}
      section={section}
      onSectionChange={setSection}
      onPreviewChange={merge}
      onUpdated={onUpdated}
    />
  )
}

/** 使用页签，访问方式与 URL 的 access 参数同步。 */
function WebsiteUsageTab({ channel, onUpdated }: { channel: WebsiteChannelDetail; onUpdated: () => void }) {
  const [access, setAccess] = useTabSearchParam("usage", "access", accessTabs)
  return (
    <WebsiteChannelUsagePanel channel={channel} access={access} onAccessChange={setAccess} onUpdated={onUpdated} />
  )
}

/** 聊天窗口实时预览。 */
function WebsitePreviewAside() {
  const { value } = useWebsitePreview()
  return <WebsiteChatPreview value={value} />
}

/** 网站渠道增加聊天窗口与使用页签，右侧显示聊天窗口实时预览。 */
export const websiteChannelEdit: ChannelEditDefinition<typeof ChannelType.Website> = {
  tabs: [
    { value: "chat-interface", form: true },
    { value: "usage", form: true },
  ],
  render: (tab, { channel, onUpdated }) =>
    tab === "usage" ? (
      <WebsiteUsageTab channel={channel} onUpdated={onUpdated} />
    ) : (
      <WebsiteChatInterfaceTab channel={channel} onUpdated={onUpdated} />
    ),
  aside: () => <WebsitePreviewAside />,
  Scope: WebsitePreviewScope,
}

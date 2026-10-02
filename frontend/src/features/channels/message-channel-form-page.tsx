/** 消息渠道创建页和按类型扩展的编辑页。 */
import { useCallback, useEffect, useMemo, useState } from "react"
import { CircleHelpIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import {
  useLocation,
  useNavigate,
  useParams,
  useSearchParams,
} from "react-router"

import {
  ChannelType,
  getMessageChannel,
  getTelegramChannel,
  getWebsiteChannel,
  isNotFoundApiError,
  TelegramWebhookStatus,
  type MessageChannelSummary,
  type TelegramChannel,
  type WebsiteChannelChatInterfaceInput,
  type WebsiteChannelData,
  type WebsiteChannelHomeInput,
} from "@/api"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ProductDocSheet } from "@/components/product-doc-sheet"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { ChannelReceptionSettingsForm } from "@/features/channels/channel-reception-settings-form"
import { MessageChannelForm } from "@/features/channels/message-channel-form"
import {
  isMessageChannelType,
  messageChannelTypeDefinition,
} from "@/lib/message-channel-types"
import { TelegramChannelConnectionForm } from "@/features/channels/telegram/telegram-channel-connection-form"
import { TelegramChannelInfoPanel } from "@/features/channels/telegram/telegram-channel-info-panel"
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
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { useReturnTo } from "@/hooks/use-return-to"

type EditTab =
  | "basic"
  | "reception"
  | "chat-interface"
  | "usage"
  | "connection"
type EditableChannel =
  | MessageChannelSummary
  | WebsiteChannelData
  | TelegramChannel

/** 各编辑页签的标题文案键。 */
const editTabLabelKeys = {
  basic: "tabs.basic",
  reception: "tabs.reception",
  "chat-interface": "tabs.chatInterface",
  usage: "tabs.usage",
  connection: "tabs.connection",
} as const satisfies Record<EditTab, string>

/** 按渠道类型给出编辑页签和详情读取方式，未列出的类型只有通用页签。 */
const channelEditConfigs: Partial<
  Record<
    ChannelType,
    {
      tabs: readonly EditTab[]
      load: (id: string, signal: AbortSignal) => Promise<EditableChannel>
    }
  >
> = {
  [ChannelType.ChannelTypeWebsite]: {
    tabs: ["basic", "reception", "chat-interface", "usage"],
    load: getWebsiteChannel,
  },
  [ChannelType.ChannelTypeTelegram]: {
    tabs: ["basic", "reception", "connection"],
    load: getTelegramChannel,
  },
}

/** 返回渠道类型的编辑配置。 */
function channelEditConfig(type: string) {
  return (
    channelEditConfigs[type as ChannelType] ?? {
      tabs: ["basic", "reception"] as const,
      load: getMessageChannel,
    }
  )
}

/** 判断值是否为渠道访问方式页签。 */
function isAccessTab(value: string | null): value is WebsiteChannelAccessTab {
  return value === "embed" || value === "link"
}

/** 判断值是否为聊天窗口子页签。 */
function isChatInterfaceSection(
  value: string | null,
): value is WebsiteChatInterfaceSection {
  return (
    value === "appearance" ||
    value === "conversation" ||
    value === "home" ||
    value === "help-center"
  )
}

/** 判断详情是否包含网站渠道扩展。 */
function isWebsiteChannelData(
  channel: EditableChannel,
): channel is WebsiteChannelData {
  return (
    channel.type === ChannelType.ChannelTypeWebsite &&
    "chatInterface" in channel &&
    "access" in channel
  )
}

/** 判断详情是否包含 Telegram 渠道扩展。 */
function isTelegramChannel(
  channel: EditableChannel,
): channel is TelegramChannel {
  return (
    channel.type === ChannelType.ChannelTypeTelegram &&
    "connection" in channel
  )
}

/** 把已保存的聊天窗口、首页与帮助中心设置归一化为实时预览值。 */
function savedPreviewValue(
  channel: WebsiteChannelData,
): WebsiteMessengerPreviewValue {
  return {
    title: channel.chatInterface.title,
    greetingMessage: channel.chatInterface.greetingMessage ?? "",
    themeColor: channel.chatInterface.themeColor,
    attachmentsEnabled: channel.chatInterface.attachmentsEnabled,
    emojiEnabled: channel.chatInterface.emojiEnabled,
    ratingEnabled: channel.chatInterface.ratingEnabled,
    multipleConversationsEnabled:
      channel.chatInterface.multipleConversationsEnabled,
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

/** 渠道编辑页签，与 URL 同步并按类型展示扩展设置。 */
function MessageChannelEditTabs({
  channel,
  onChannelChange,
  onConnectionSavingChange,
}: {
  channel: EditableChannel
  onChannelChange: () => void
  onConnectionSavingChange: (saving: boolean) => void
}) {
  const { t } = useTranslation("channels")
  const [searchParams, setSearchParams] = useSearchParams()
  const websiteChannel = isWebsiteChannelData(channel) ? channel : null
  const telegramChannel = isTelegramChannel(channel) ? channel : null
  const { tabs } = channelEditConfig(channel.type)
  const requestedTab = searchParams.get("tab")
  const requestedAccess = searchParams.get("access")
  const requestedSection = searchParams.get("section")
  const tabValid = tabs.some((tab) => tab === requestedTab)
  const activeTab = tabValid ? (requestedTab as EditTab) : "basic"
  const activeAccess: WebsiteChannelAccessTab =
    requestedAccess === "link" ? "link" : "embed"
  const activeSection: WebsiteChatInterfaceSection = isChatInterfaceSection(
    requestedSection,
  )
    ? requestedSection
    : "appearance"
  // 预览草稿由聊天窗口、首页与帮助中心三份表单上报合并，初始值取已保存设置。
  const [previewDraft, setPreviewDraft] =
    useState<WebsiteMessengerPreviewValue | null>(() =>
      websiteChannel ? savedPreviewValue(websiteChannel) : null,
    )
  const mergePreviewValue = useCallback(
    (
      value:
        | WebsiteChannelChatInterfaceInput
        | WebsiteChannelHomeInput
        | WebsiteHelpCenterPreviewDraft,
    ) =>
      setPreviewDraft((current) => (current ? { ...current, ...value } : null)),
    [],
  )
  // 发布的知识库取渠道详情中的已保存值，详情刷新后随之更新。
  const savedKnowledgeBaseIds = websiteChannel?.helpCenter.knowledgeBaseIds
  const previewValue = useMemo(
    () =>
      previewDraft && savedKnowledgeBaseIds
        ? { ...previewDraft, helpKnowledgeBaseIds: savedKnowledgeBaseIds }
        : null,
    [previewDraft, savedKnowledgeBaseIds],
  )

  useEffect(() => {
    const accessValid = isAccessTab(requestedAccess)
    const sectionValid = isChatInterfaceSection(requestedSection)
    if (
      tabValid &&
      (activeTab !== "usage" || accessValid) &&
      (activeTab !== "chat-interface" || sectionValid) &&
      (websiteChannel || (requestedAccess === null && requestedSection === null))
    ) {
      return
    }
    const nextParams = new URLSearchParams(searchParams)
    if (!tabValid) {
      nextParams.set("tab", "basic")
    }
    if (websiteChannel && activeTab === "usage" && !accessValid) {
      nextParams.set("access", "embed")
    }
    if (websiteChannel && activeTab === "chat-interface" && !sectionValid) {
      nextParams.set("section", "appearance")
    }
    if (!websiteChannel) {
      nextParams.delete("access")
      nextParams.delete("section")
    }
    setSearchParams(nextParams, { replace: true })
  }, [
    activeTab,
    requestedAccess,
    requestedSection,
    searchParams,
    setSearchParams,
    tabValid,
    websiteChannel,
  ])

  /** 切换渠道编辑页签并同步 URL。 */
  function setTab(value: string) {
    const nextParams = new URLSearchParams(searchParams)
    nextParams.set("tab", value)
    if (value === "usage" && !isAccessTab(nextParams.get("access"))) {
      nextParams.set("access", "embed")
    }
    if (
      value === "chat-interface" &&
      !isChatInterfaceSection(nextParams.get("section"))
    ) {
      nextParams.set("section", "appearance")
    }
    setSearchParams(nextParams, { replace: true })
  }

  /** 切换网站渠道访问方式并同步 URL。 */
  function setAccess(value: WebsiteChannelAccessTab) {
    const nextParams = new URLSearchParams(searchParams)
    nextParams.set("tab", "usage")
    nextParams.set("access", value)
    setSearchParams(nextParams, { replace: true })
  }

  /** 切换聊天窗口子页签并同步 URL。 */
  function setSection(value: WebsiteChatInterfaceSection) {
    const nextParams = new URLSearchParams(searchParams)
    nextParams.set("tab", "chat-interface")
    nextParams.set("section", value)
    setSearchParams(nextParams, { replace: true })
  }

  const content = (
    <div className="min-w-0">
      <TabsContent
        value="basic"
        forceMount
        className="data-[state=inactive]:hidden"
      >
        <MessageChannelForm channel={channel} onUpdated={onChannelChange} />
      </TabsContent>
      <TabsContent
        value="reception"
        forceMount
        className="data-[state=inactive]:hidden"
      >
        <ChannelReceptionSettingsForm
          channel={channel}
          onUpdated={onChannelChange}
        />
      </TabsContent>
      {websiteChannel ? (
        <>
          <TabsContent
            value="chat-interface"
            forceMount
            className="data-[state=inactive]:hidden"
          >
            <WebsiteChannelChatInterfacePanel
              channel={websiteChannel}
              section={activeSection}
              onSectionChange={setSection}
              onPreviewChange={mergePreviewValue}
              onUpdated={onChannelChange}
            />
          </TabsContent>
          <TabsContent
            value="usage"
            forceMount
            className="data-[state=inactive]:hidden"
          >
            <WebsiteChannelUsagePanel
              channel={websiteChannel}
              access={activeAccess}
              onAccessChange={setAccess}
              onUpdated={onChannelChange}
            />
          </TabsContent>
        </>
      ) : null}
      {telegramChannel ? (
        <TabsContent
          value="connection"
          forceMount
          className="data-[state=inactive]:hidden"
        >
          <TelegramChannelConnectionForm
            channel={telegramChannel}
            onUpdated={onChannelChange}
            onSavingChange={onConnectionSavingChange}
          />
        </TabsContent>
      ) : null}
    </div>
  )

  // 右侧面板：Telegram 显示接入信息，网站渠道显示聊天窗口实时预览。
  const aside = telegramChannel ? (
    <TelegramChannelInfoPanel
      channel={telegramChannel}
      onUpdated={onChannelChange}
    />
  ) : websiteChannel && previewValue ? (
    <WebsiteChatPreview value={previewValue} />
  ) : null

  return (
    <Tabs value={activeTab} onValueChange={setTab}>
      <TabsList>
        {tabs.map((tab) => (
          <TabsTrigger key={tab} value={tab}>
            {t(editTabLabelKeys[tab])}
          </TabsTrigger>
        ))}
      </TabsList>
      {aside ? (
        <div className="mt-6 grid gap-8 xl:grid-cols-[minmax(0,1fr)_360px]">
          {content}
          {aside}
        </div>
      ) : (
        <div className="mt-6">{content}</div>
      )}
    </Tabs>
  )
}

/** 创建或编辑消息渠道。 */
export function MessageChannelFormPage({
  mode,
}: {
  mode: "create" | "edit"
}) {
  const { t } = useTranslation(["channels", "common"])
  const navigate = useNavigate()
  const location = useLocation()
  const { channelId = "", channelType = "" } = useParams()
  const [telegramConnectionSaving, setTelegramConnectionSaving] =
    useState(false)
  const editable = mode === "edit" && isMessageChannelType(channelType)
  const detailKey = resourceKeys.messageChannel(channelType, channelId)
  const detail = useResource<EditableChannel>(
    detailKey,
    (signal) => channelEditConfig(channelType).load(channelId, signal),
    {
      enabled: editable,
      // 等待 Telegram 回调时低频刷新已保存的 Webhook 状态。
      refetchInterval: (data) =>
        data &&
        isTelegramChannel(data) &&
        data.connection.webhookStatus ===
          TelegramWebhookStatus.TelegramWebhookStatusWaiting &&
        !telegramConnectionSaving
          ? 8_000
          : false,
    },
  )
  // 地址中的类型与详情一致时才展示，类型不一致时等待纠正地址。
  const channel =
    detail.data && detail.data.type === channelType ? detail.data : null
  const invalidateResource = useResourceInvalidator()

  // 渠道不存在时回到来源列表。
  const { returnTo, leave } = useReturnTo("/channels", {
    notFound: Boolean(detail.error && isNotFoundApiError(detail.error)),
    logFields: { channel_id: channelId, channel_type: channelType },
  })

  /** 拦截无效的渠道类型参数，回到来源列表。 */
  useEffect(() => {
    if (!isMessageChannelType(channelType)) {
      leave({ replace: true })
    }
  }, [channelType, leave])

  /** 详情类型与地址不一致时校正地址。 */
  const loadedChannel = detail.data
  useEffect(() => {
    if (!loadedChannel || loadedChannel.type === channelType) return
    navigate(`/channels/${loadedChannel.type}/${loadedChannel.id}${location.search}`, {
      replace: true,
    })
  }, [channelType, loadedChannel, location.search, navigate])

  /** 子表单保存后重新读取服务端详情。 */
  function handleChannelChange() {
    void invalidateResource(detailKey)
  }

  const typeDefinition = channel
    ? messageChannelTypeDefinition(channel.type)
    : isMessageChannelType(channelType)
      ? messageChannelTypeDefinition(channelType)
      : undefined
  const typeLabel = typeDefinition
    ? t(`types.${typeDefinition.translationKey}`)
    : ""
  const docsPage = typeDefinition?.docsPage
  // 打开帮助侧栏的按钮，非空时侧栏打开。
  const [docsTrigger, setDocsTrigger] = useState<HTMLElement | null>(null)
  const editTitle = typeLabel
    ? t("edit.title", { type: typeLabel })
    : t("edit.fallbackTitle")

  return (
    <div className="flex min-h-0 w-full flex-1 flex-col overflow-hidden">
      <PageHeader
        title={
          mode === "create"
            ? t("create.title", { type: typeLabel })
            : channel && typeLabel
              ? t("edit.namedTitle", { type: typeLabel, name: channel.name })
              : editTitle
        }
        description={t(
          mode === "create" ? "create.description" : "edit.description",
        )}
        backTo={mode === "edit" ? returnTo : undefined}
      >
        {docsPage ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={(event) => setDocsTrigger(event.currentTarget)}
          >
            <CircleHelpIcon />
            {t("common:productDocs")}
          </Button>
        ) : null}
      </PageHeader>
      <PageContent variant="form">
        {mode === "edit" ? (
          <ResourceContent
            resources={editable ? detail : []}
            errorMessage={t("form.loadError")}
          >
            {channel ? (
              <MessageChannelEditTabs
                key={channel.id}
                channel={channel}
                onChannelChange={handleChannelChange}
                onConnectionSavingChange={setTelegramConnectionSaving}
              />
            ) : null}
          </ResourceContent>
        ) : isMessageChannelType(channelType) ? (
          <MessageChannelForm type={channelType} />
        ) : null}
      </PageContent>
      <ProductDocSheet page={docsTrigger && docsPage ? docsPage : null} trigger={docsTrigger} onClose={() => setDocsTrigger(null)} />
    </div>
  )
}

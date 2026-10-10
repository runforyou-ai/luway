/** 消息渠道创建页和按渠道类型登记扩展的编辑页。 */
import { useEffect, useState } from "react"
import { CircleHelpIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useParams, useSearchParams } from "react-router"

import {
  getMessageChannelDetail,
  isNotFoundApiError,
  type ChannelType,
  type MessageChannelDetail,
} from "@/api"
import { PageContent } from "@/components/page-content"
import { PageHeader } from "@/components/page-header"
import { ProductDocSheet } from "@/components/product-doc-sheet"
import { ResourceContent } from "@/components/resource-content"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { ChannelReceptionSettingsForm } from "@/features/channels/channel-reception-settings-form"
import type { ChannelEditContext, ChannelEditTab } from "@/features/channels/channel-edit-definition"
import { channelEditDefinition } from "@/features/channels/channel-edit-registry"
import { MessageChannelForm } from "@/features/channels/message-channel-form"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource, useResourceInvalidator } from "@/hooks/use-resource"
import { useReturnTo } from "@/hooks/use-return-to"
import { isMessageChannelType, messageChannelTypeDefinition } from "@/lib/message-channel-types"

/** 各编辑页签的标题文案键。 */
const editTabLabelKeys = {
  basic: "tabs.basic",
  reception: "tabs.reception",
  "chat-interface": "tabs.chatInterface",
  usage: "tabs.usage",
  connection: "tabs.connection",
  members: "tabs.members",
} as const satisfies Record<ChannelEditTab, string>

/** 返回渠道详情的自动刷新间隔，由渠道类型的编辑页定义决定。 */
function channelRefetchInterval<T extends ChannelType>(channel: MessageChannelDetail<T>, saving: boolean) {
  return channelEditDefinition<T>(channel.type).refetchInterval?.(channel, saving) ?? false
}

/** 渠道编辑页签：通用页签之后接渠道类型登记的扩展页签，当前页签与 URL 同步。 */
function MessageChannelEditTabs<T extends ChannelType>({
  channel,
  onChannelChange,
  onConnectionSavingChange,
}: {
  channel: MessageChannelDetail<T>
  onChannelChange: () => void
  onConnectionSavingChange: (saving: boolean) => void
}) {
  const { t } = useTranslation("channels")
  const [searchParams, setSearchParams] = useSearchParams()
  const definition = channelEditDefinition<T>(channel.type)
  const tabs: ChannelEditTab[] = ["basic", "reception", ...definition.tabs.map((tab) => tab.value)]
  const requestedTab = searchParams.get("tab")
  const activeTab = tabs.find((tab) => tab === requestedTab) ?? "basic"
  const context: ChannelEditContext<T> = {
    channel,
    onUpdated: onChannelChange,
    onSavingChange: onConnectionSavingChange,
  }

  // 地址中的页签无效时改写为基本信息页签。
  useEffect(() => {
    if (requestedTab === activeTab) return
    const nextParams = new URLSearchParams(searchParams)
    nextParams.set("tab", activeTab)
    setSearchParams(nextParams, { replace: true })
  }, [activeTab, requestedTab, searchParams, setSearchParams])

  /** 切换渠道编辑页签并同步 URL。 */
  function setTab(value: string) {
    const nextParams = new URLSearchParams(searchParams)
    nextParams.set("tab", value)
    setSearchParams(nextParams, { replace: true })
  }

  const content = (
    <div className="min-w-0">
      <TabsContent value="basic" forceMount className="data-[state=inactive]:hidden">
        <MessageChannelForm channel={channel} onUpdated={onChannelChange} />
      </TabsContent>
      <TabsContent value="reception" forceMount className="data-[state=inactive]:hidden">
        <ChannelReceptionSettingsForm channel={channel} onUpdated={onChannelChange} />
      </TabsContent>
      {definition.tabs.map((tab) =>
        tab.form ? (
          <TabsContent key={tab.value} value={tab.value} forceMount className="data-[state=inactive]:hidden">
            {definition.render(tab.value, context)}
          </TabsContent>
        ) : (
          <TabsContent key={tab.value} value={tab.value}>
            {definition.render(tab.value, context)}
          </TabsContent>
        ),
      )}
    </div>
  )
  const aside = definition.aside?.(context)
  const body = (
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
  const Scope = definition.Scope
  return Scope ? <Scope channel={channel}>{body}</Scope> : body
}

/** 创建或编辑消息渠道。 */
export function MessageChannelFormPage({ mode }: { mode: "create" | "edit" }) {
  const { t } = useTranslation(["channels", "common"])
  const { channelId = "", channelType = "" } = useParams()
  const [connectionSaving, setConnectionSaving] = useState(false)
  const routeType = isMessageChannelType(channelType) ? channelType : null
  const editable = mode === "edit" && routeType !== null
  const detailKey = resourceKeys.messageChannel(channelId, channelType)
  const detail = useResource<MessageChannelDetail>(
    detailKey,
    (signal) => getMessageChannelDetail(routeType!, channelId, signal),
    {
      enabled: editable,
      refetchInterval: (data) => (data ? channelRefetchInterval(data, connectionSaving) : false),
    },
  )
  const channel = detail.data ?? null
  const invalidateResource = useResourceInvalidator()

  // 渠道不存在时回到来源列表。
  const { returnTo, leave } = useReturnTo("/channels", {
    notFound: Boolean(detail.error && isNotFoundApiError(detail.error)),
    logFields: { channel_id: channelId, channel_type: channelType },
  })

  // 拦截无效的渠道类型参数，回到来源列表。
  useEffect(() => {
    if (!routeType) {
      leave({ replace: true })
    }
  }, [routeType, leave])

  /** 子表单保存后重新读取服务端详情。 */
  function handleChannelChange() {
    void invalidateResource(detailKey)
  }

  const typeDefinition = routeType ? messageChannelTypeDefinition(routeType) : undefined
  const typeLabel = typeDefinition ? t(`types.${typeDefinition.translationKey}`) : ""
  const docsPage = typeDefinition?.docsPage
  // 打开帮助侧栏的按钮，非空时侧栏打开。
  const [docsTrigger, setDocsTrigger] = useState<HTMLElement | null>(null)
  const editTitle = typeLabel ? t("edit.title", { type: typeLabel }) : t("edit.fallbackTitle")

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
        description={t(mode === "create" ? "create.description" : "edit.description")}
        backTo={mode === "edit" ? returnTo : undefined}
      >
        {docsPage ? (
          <Button type="button" variant="ghost" size="sm" onClick={(event) => setDocsTrigger(event.currentTarget)}>
            <CircleHelpIcon />
            {t("common:productDocs")}
          </Button>
        ) : null}
      </PageHeader>
      <PageContent variant="form">
        {mode === "edit" ? (
          <ResourceContent resources={editable ? detail : []} errorMessage={t("form.loadError")}>
            {channel ? (
              <MessageChannelEditTabs
                key={channel.id}
                channel={channel}
                onChannelChange={handleChannelChange}
                onConnectionSavingChange={setConnectionSaving}
              />
            ) : null}
          </ResourceContent>
        ) : routeType ? (
          <MessageChannelForm type={routeType} />
        ) : null}
      </PageContent>
      <ProductDocSheet page={docsTrigger && docsPage ? docsPage : null} trigger={docsTrigger} onClose={() => setDocsTrigger(null)} />
    </div>
  )
}

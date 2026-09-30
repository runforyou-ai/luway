/** 客服、单聊与群聊共用的会话侧边面板。 */
import { PanelRightCloseIcon } from "lucide-react"
import { useEffect, useRef, useState, type RefObject } from "react"
import { useTranslation } from "react-i18next"

import {
  ChannelType,
  ServiceSource,
  isServiceInboxConversation,
  isAgentInboxConversation,
  isGroupInboxConversation,
  type InboxConversationData,
  type MemberOption,
} from "@/api"
import { ResizeHandle } from "@/components/resize-handle"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { ConversationAvatar } from "@/features/inbox/conversation-avatar"
import { DirectConversationDraftAvatar } from "@/features/inbox/direct-conversation-draft-header"
import type { ComposerDraftBridge } from "@/features/inbox/conversation-composer-types"
import { ServiceBusinessQueries } from "@/features/inbox/customer-business-queries"
import { CustomerServiceHistory } from "@/features/inbox/customer-service-history"
import { ServiceCopilotPanel } from "@/features/inbox/customer-copilot-panel"
import { CustomerProfileDetails } from "@/features/inbox/customer-profile-details"
import { GroupConversationContext } from "@/features/inbox/group-conversation-context"
import { HeaderAction } from "@/features/inbox/conversation-header"
import { InternalConversationDetails } from "@/features/inbox/internal-conversation-details"
import {
  SidePanelField,
  SidePanelTab,
  SidePanelTabsList,
} from "@/features/inbox/side-panel-layout"
import { cn } from "@/lib/utils"

const sidePanelMinWidth = 320
const sidePanelDefaultWidth = 384
const sidePanelMaxWidth = 640
// 展开侧边面板后为会话区保留的宽度，空间不足时侧边面板最多占一半。
const conversationMinWidth = 420

/** 展示单聊与 AI 会话的基础资料。 */
function InternalConversationProfile({
  conversation,
  directTarget,
  displayName,
}: {
  conversation: InboxConversationData | null
  directTarget: MemberOption | null
  displayName: string
}) {
  const { t } = useTranslation("inbox")
  const agent =
    conversation && isAgentInboxConversation(conversation)
      ? conversation.agent
      : null

  return (
    <dl className="space-y-1 text-sm">
      <SidePanelField label={t("contextContactName")}>
        {conversation ? (
          <ConversationAvatar
            conversation={conversation}
            className="size-7"
          />
        ) : directTarget ? (
          <DirectConversationDraftAvatar
            member={directTarget}
            className="size-7"
          />
        ) : null}
        <span
          className="min-w-0 truncate"
          title={agent?.agentName ?? displayName}
        >
          {agent?.agentName ?? displayName}
        </span>
      </SidePanelField>
      <InternalConversationDetails
        conversation={conversation}
        directTarget={directTarget}
      />
    </dl>
  )
}

/** 展示当前会话摘要和类型对应的资料内容。 */
function ConversationSidePanelContent({
  conversation,
  directTarget,
  displayName,
  currentIdentityID,
  replyDisabledReason,
  customerDraftRef,
  onGroupLeft,
  onClose,
}: {
  conversation: InboxConversationData | null
  directTarget: MemberOption | null
  displayName: string
  currentIdentityID: string
  replyDisabledReason: string | null
  customerDraftRef: RefObject<ComposerDraftBridge | null>
  onGroupLeft: () => void
  onClose: () => void
}) {
  const { t } = useTranslation("inbox")
  const [customerTab, setCustomerTab] = useState("profile")
  const customer =
    conversation && isServiceInboxConversation(conversation)
      ? conversation.service
      : null
  const group =
    conversation && isGroupInboxConversation(conversation)
      ? conversation.group
      : null
  // 渠道来源提供客户资料、AI 助手与业务查询，其他来源只展示发起人与服务记录。
  const channelSource = customer?.source === ServiceSource.ServiceSourceChannel
  return (
    <div className="relative flex h-full min-h-0 min-w-0 flex-col overflow-x-visible overflow-y-hidden bg-background">
      {/* 页签行右端常驻收起入口，三类上下文共用同一位置。 */}
      <HeaderAction
        label={t("sidePanelClose")}
        icon={PanelRightCloseIcon}
        className="absolute top-2.5 right-2 z-10"
        onClick={onClose}
      />
      {conversation && customer ? (
        <Tabs
          key={conversation.id}
          value={channelSource ? customerTab : "profile"}
          onValueChange={setCustomerTab}
          className="min-h-0 flex-1"
        >
          <SidePanelTabsList aria-label={t("contextTabsLabel")}>
            <SidePanelTab value="profile">
              {t("contextProfileTab")}
            </SidePanelTab>
            {channelSource ? (
              <>
                <SidePanelTab value="assistant">
                  {t("contextAssistantTab")}
                </SidePanelTab>
                <SidePanelTab value="business">
                  {t("contextBusinessTab")}
                </SidePanelTab>
              </>
            ) : null}
          </SidePanelTabsList>

          <TabsContent
            value="profile"
            className="mt-0 min-h-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain p-3"
          >
            <dl className="space-y-1 text-sm">
              <SidePanelField label={t("contextContactName")}>
                <ConversationAvatar
                  conversation={conversation}
                  className="size-7"
                />
                <span className="min-w-0 truncate" title={displayName}>
                  {displayName}
                </span>
              </SidePanelField>
            </dl>
            {channelSource ? (
              <CustomerProfileDetails
                conversationID={conversation.id}
                website={customer.channel?.type === ChannelType.ChannelTypeWebsite}
              />
            ) : null}
            <CustomerServiceHistory conversationID={conversation.id} />
          </TabsContent>

          {channelSource ? (
            <>
              <TabsContent
                value="assistant"
                forceMount
                className="mt-0 flex min-h-0 flex-1 flex-col overflow-hidden data-[state=inactive]:hidden"
              >
                <ServiceCopilotPanel
                  key={conversation.id}
                  servedConversationID={conversation.id}
                  replyDisabledReason={replyDisabledReason}
                  customerDraftRef={customerDraftRef}
                  active={customerTab === "assistant"}
                />
              </TabsContent>

              <TabsContent
                value="business"
                className="mt-0 min-h-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain"
              >
                <ServiceBusinessQueries conversationID={conversation.id} />
              </TabsContent>
            </>
          ) : null}
        </Tabs>
      ) : conversation && group ? (
        <GroupConversationContext
          conversationID={conversation.id}
          currentIdentityID={currentIdentityID}
          onLeft={onGroupLeft}
        />
      ) : (
        <Tabs
          key={conversation?.id ?? directTarget?.id}
          defaultValue="profile"
          className="min-h-0 flex-1"
        >
          <SidePanelTabsList aria-label={t("contextTabsLabel")}>
            <SidePanelTab value="profile">
              {t("contextProfileTab")}
            </SidePanelTab>
          </SidePanelTabsList>
          <TabsContent
            value="profile"
            className="mt-0 min-h-0 flex-1 overflow-y-auto overscroll-contain p-3"
          >
            <InternalConversationProfile
              conversation={conversation}
              directTarget={directTarget}
              displayName={displayName}
            />
          </TabsContent>
        </Tabs>
      )}
    </div>
  )
}

/** 展示可调整宽度和收起状态的会话侧边面板。 */
export function ConversationSidePanel({
  conversation,
  directTarget,
  displayName,
  currentIdentityID,
  replyDisabledReason,
  customerDraftRef,
  onGroupLeft,
  visible,
  onClose,
}: {
  conversation: InboxConversationData | null
  directTarget: MemberOption | null
  displayName: string
  currentIdentityID: string
  replyDisabledReason: string | null
  customerDraftRef: RefObject<ComposerDraftBridge | null>
  onGroupLeft: () => void
  visible: boolean
  onClose: () => void
}) {
  const { t } = useTranslation("inbox")
  const trackRef = useRef<HTMLDivElement>(null)
  const [rowWidth, setRowWidth] = useState(0)
  // 展开宽度默认取默认档位，剩余空间不足时按会话区的保留宽度收窄。
  const [desiredWidth, setDesiredWidth] = useState(sidePanelDefaultWidth)
  const maxWidth = rowWidth
    ? Math.min(sidePanelMaxWidth, Math.max(Math.round(rowWidth / 2), rowWidth - conversationMinWidth))
    : sidePanelMaxWidth
  const sidePanelWidth = Math.min(desiredWidth, maxWidth)

  useEffect(() => {
    const row = trackRef.current?.parentElement
    if (!row) return
    const observer = new ResizeObserver(([entry]) => setRowWidth(entry.contentRect.width))
    observer.observe(row)
    return () => observer.disconnect()
  }, [])

  return (
    <>
      <div ref={trackRef} className="relative h-full min-h-0 w-0 shrink-0">
        {visible ? (
          <ResizeHandle
            label={t("sidePanelResize")}
            className="z-20 -translate-x-1"
            onResize={(clientX) => {
              // 按指针位置调整宽度，下限为最小宽度与当前允许最大值中较小的一个。
              const width = Math.max(
                Math.min(sidePanelMinWidth, maxWidth),
                window.innerWidth - clientX,
              )
              setDesiredWidth(Math.min(maxWidth, width))
            }}
          />
        ) : null}
      </div>

      <aside
        className={cn(
          "app-conversation-side-panel relative h-full min-h-0 min-w-0 shrink-0 overflow-hidden border-l bg-background",
          !visible && "hidden",
        )}
        style={{ width: sidePanelWidth }}
      >
        <ConversationSidePanelContent
          conversation={conversation}
          directTarget={directTarget}
          displayName={displayName}
          currentIdentityID={currentIdentityID}
          replyDisabledReason={replyDisabledReason}
          customerDraftRef={customerDraftRef}
          onGroupLeft={onGroupLeft}
          onClose={onClose}
        />
      </aside>
    </>
  )
}

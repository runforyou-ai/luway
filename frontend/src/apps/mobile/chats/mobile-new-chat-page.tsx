/** 移动端发起单聊：选择同事、AI 员工，打开已有单聊或新草稿。 */
import { useState } from "react"
import { ChevronRightIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useLocation, useNavigate, useParams } from "react-router"

import { WorkspaceIdentityType, type DirectInboxConversationData, type MemberOption } from "@/api"
import type { MobileAgentLocationState } from "@/apps/mobile/chats/mobile-agent-chat-page"
import type { MobileIndividualLocationState } from "@/apps/mobile/chats/mobile-individual-conversation-page"
import { useMobileBack } from "@/apps/mobile/shared/mobile-navigation"
import {
  MobilePageHeader,
  MobilePageState,
  MobileScrollArea,
  MobileSearchBar,
} from "@/apps/mobile/shared/mobile-page"
import { useMobileWorkspace } from "@/apps/mobile/shared/mobile-workspace-layout"
import { LoadingIndicator } from "@/components/loading-indicator"
import { DirectConversationDraftAvatar } from "@/features/inbox/conversation/direct-conversation-draft-header"
import { InboxConversationTarget } from "@/features/inbox/conversation/inbox-conversation-target"
import {
  filterChatTargets,
  listChatTargets,
  orderChatTargets,
} from "@/features/inbox/shared/list-all-member-options"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** 列出可发起单聊的对象并按姓名筛选，选择后以目标页替换当前页。 */
export function MobileNewChatPage() {
  const { t } = useTranslation(["inbox", "common"])
  const { identity } = useMobileWorkspace()
  const navigate = useNavigate()
  const [search, setSearch] = useState("")
  const { data, loading, error, refresh } = useResource(
    resourceKeys.chatTargets(),
    listChatTargets,
    { staleTime: 0 },
  )
  const candidates = filterChatTargets(orderChatTargets(data ?? [], identity.user.identityId), search)

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader title={t("newDirectConversation")} backTo="/chats" />
      <MobileSearchBar
        label={t("chatPickerSearch")}
        value={search}
        onChange={setSearch}
      />
      <MobileScrollArea storageKey="chats:new" ready={data !== undefined}>
        {loading ? (
          <LoadingIndicator className="min-h-64 justify-center">
            {t("chatPickerLoading")}
          </LoadingIndicator>
        ) : error ? (
          <MobilePageState
            title={t("chatPickerLoadError")}
            onRetry={() => void refresh()}
          />
        ) : candidates.length === 0 ? (
          <p className="px-6 py-12 text-center text-sm text-muted-foreground">
            {t(search.trim() ? "membersNoMatches" : "chatPickerEmpty")}
          </p>
        ) : (
          <ul className="divide-y border-b">
            {candidates.map((member) => (
              <li key={member.id}>
                <button
                  type="button"
                  className="flex min-h-18 w-full items-center gap-3 px-4 py-3 text-left outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
                  onClick={() =>
                    void navigate(`/chats/new/${member.id}`, {
                      replace: true,
                      state: { mobileBack: true },
                    })
                  }
                >
                  <DirectConversationDraftAvatar member={member} className="size-10" />
                  <span className="min-w-0 flex-1 truncate text-[15px] font-medium">
                    {member.displayName}
                  </span>
                  {member.type === WorkspaceIdentityType.Agent ? (
                    <span className="shrink-0 text-xs text-muted-foreground">
                      {t(member.personal ? "chatPickerPersonalAgent" : "chatPickerAgent")}
                    </span>
                  ) : null}
                  <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground" />
                </button>
              </li>
            ))}
          </ul>
        )}
      </MobileScrollArea>
    </section>
  )
}

/** 按单聊对象隔离解析结果和草稿。 */
export function MobileNewChatTargetPage() {
  const { identityID = "" } = useParams()
  return <MobileNewChatTarget key={identityID} identityID={identityID} />
}

/** 解析单聊对象：同事复用已有单聊或进入草稿，AI 员工进入新的 AI 对话。 */
function MobileNewChatTarget({ identityID }: { identityID: string }) {
  const { t } = useTranslation(["inbox", "common"])
  const { identity } = useMobileWorkspace()
  const navigate = useNavigate()
  const location = useLocation()
  const back = useMobileBack("/chats")

  /** 已有单聊直接打开，同事无单聊时进入真人单聊草稿，AI 对象带着已解析目标进入 AI 对话。 */
  function openTarget(
    member: MemberOption,
    conversation: DirectInboxConversationData | null,
  ) {
    if (conversation) {
      void navigate(`/chats/direct/${conversation.id}`, {
        replace: true,
        state: { ...location.state, conversation },
      })
    } else if (member.type === WorkspaceIdentityType.User) {
      void navigate(`/chats/direct/${crypto.randomUUID()}`, {
        replace: true,
        state: {
          ...location.state,
          draftPeer: { identityId: member.id, displayName: member.displayName },
        } satisfies MobileIndividualLocationState,
      })
    } else {
      void navigate(`/chats/agent/${crypto.randomUUID()}`, {
        replace: true,
        state: {
          ...location.state,
          draftTarget: { identityId: member.id, displayName: member.displayName },
        } satisfies MobileAgentLocationState,
      })
    }
  }

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader
        title={t("newDirectConversation")}
        backTo="/chats"
      />
      <LoadingIndicator className="min-h-0 flex-1 justify-center">
        {t("common:status.loading")}
      </LoadingIndicator>
      <InboxConversationTarget
        identityId={identityID}
        currentIdentityId={identity.user.identityId}
        onSelected={openTarget}
        onFailed={back}
      />
    </section>
  )
}

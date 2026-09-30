/** 从成员资料进入已有单聊或尚未发送的草稿。 */
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { Navigate, useLocation, useNavigate, useParams } from "react-router"

import {
  findDirectConversation,
  getUser,
  isDirectInboxConversation,
  type DirectInboxConversationData,
  UserStatus,
  type UserData,
} from "@/api"
import { MobileIndividualThread } from "@/apps/mobile/mobile-individual-thread"
import { MobilePageHeader, MobilePageState } from "@/apps/mobile/mobile-page"
import { useMobileWorkspace } from "@/apps/mobile/mobile-workspace-layout"
import { LoadingIndicator } from "@/components/loading-indicator"
import { useFirstChatMessage } from "@/features/inbox/use-first-chat-message"
import { resourceKeys } from "@/hooks/resource-keys"
import { useMountedRef } from "@/hooks/use-mounted-ref"
import { useResource } from "@/hooks/use-resource"

/** 按成员隔离草稿生命周期和发送结果。 */
export function MobileEmployeeChatPage() {
  const { userID = "" } = useParams()
  return <MobileEmployeeChat key={userID} userID={userID} />
}

/** 草稿建立后卸载入口查询并固定输入区。 */
function MobileEmployeeChat({ userID }: { userID: string }) {
  const [draftUser, setDraftUser] = useState<UserData | null>(null)
  if (!draftUser) {
    return <MobileEmployeeChatLookup userID={userID} onDraft={setDraftUser} />
  }
  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader
        title={draftUser.displayName}
        backTo={`/contacts/employees/${userID}`}
      />
      <MobileDirectDraft
        identityID={draftUser.identityId}
        memberUserID={draftUser.id}
      />
    </section>
  )
}

/** 确认成员有效并查找已有单聊。 */
function MobileEmployeeChatLookup({
  userID,
  onDraft,
}: {
  userID: string
  onDraft: (user: UserData) => void
}) {
  const { t } = useTranslation(["mobile", "common"])
  const { identity } = useMobileWorkspace()
  const location = useLocation()
  const profileURL = `/contacts/employees/${userID}`
  const member = useResource(resourceKeys.user(userID), () => getUser(userID), {
    staleTime: 0,
  })
  const user = member.data
  const canSend =
    user?.status === UserStatus.UserStatusActive &&
    user.identityId !== identity.user.identityId
  const lookup = useResource(
    resourceKeys.directConversation(user?.identityId ?? ""),
    () => findDirectConversation(user?.identityId ?? ""),
    {
      enabled: canSend && !member.error,
      staleTime: 0,
      refetchOnWindowFocus: false,
    },
  )

  useEffect(() => {
    // 只在本次成员校验和会话查找完成后交接草稿，发送资格仍由服务端校验。
    if (
      user &&
      canSend &&
      !member.error &&
      !member.refreshing &&
      !lookup.error &&
      !lookup.loading &&
      !lookup.refreshing &&
      lookup.data === null
    ) {
      onDraft(user)
    }
  }, [
    user,
    canSend,
    member.error,
    member.refreshing,
    lookup.error,
    lookup.loading,
    lookup.refreshing,
    lookup.data,
    onDraft,
  ])

  if (
    canSend &&
    !member.error &&
    !member.refreshing &&
    !lookup.error &&
    lookup.data &&
    !lookup.refreshing
  ) {
    return (
      <Navigate
        to={`/chats/direct/${lookup.data.id}`}
        replace
        state={{
          ...location.state,
          conversation: lookup.data,
          memberUserID: userID,
        }}
      />
    )
  }

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader
        title={user?.displayName ?? t("contacts.sendMessage")}
        backTo={profileURL}
      />
      {member.error || lookup.error ? (
        <MobilePageState
          title={t("contacts.chatError")}
          onRetry={() =>
            void (member.error ? member.refresh() : lookup.refresh())
          }
        />
      ) : user && !canSend ? (
        <MobilePageState title={t("contacts.chatUnavailable")} />
      ) : (
        <LoadingIndicator className="min-h-0 flex-1 justify-center">
          {t("common:status.loading")}
        </LoadingIndicator>
      )}
    </section>
  )
}

/** 真人单聊草稿，首次发送成功后用正式会话替换草稿路由；给出成员编号时保留成员资料作为返回位置。 */
export function MobileDirectDraft({
  identityID,
  memberUserID,
}: {
  identityID: string
  memberUserID?: string
}) {
  const navigate = useNavigate()
  const location = useLocation()
  const firstChat = useFirstChatMessage()
  const alive = useMountedRef()

  /** 首发确认后替换当前草稿路由。 */
  function handleCreated(conversation: DirectInboxConversationData) {
    if (alive.current) {
      void navigate(`/chats/direct/${conversation.id}`, {
        replace: true,
        state: { ...location.state, conversation, memberUserID },
      })
    }
  }

  return (
    <MobileIndividualThread
      conversationID=""
      peerIdentityID={identityID}
      onAttachmentConversationCreated={(conversation) => {
        if (!isDirectInboxConversation(conversation)) return
        firstChat.refreshStarted(conversation, identityID)
        handleCreated(conversation)
      }}
      sendIndividualMessage={async (input) => {
        const result = await firstChat.sendDirect(identityID, input)
        handleCreated(result.conversation)
        return result.message
      }}
    />
  )
}

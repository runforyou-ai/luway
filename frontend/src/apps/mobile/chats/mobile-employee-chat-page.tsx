/** 从成员资料进入已有单聊或尚未发送的草稿。 */
import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Navigate, useLocation, useParams } from "react-router"

import { findDirectConversation, getUser, UserStatus } from "@/api"
import type { MobileIndividualLocationState } from "@/apps/mobile/chats/mobile-individual-conversation-page"
import { MobilePageHeader, MobilePageState } from "@/apps/mobile/shared/mobile-page"
import { useMobileWorkspace } from "@/apps/mobile/shared/mobile-workspace-layout"
import { LoadingIndicator } from "@/components/loading-indicator"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** 按成员隔离入口查询。 */
export function MobileEmployeeChatPage() {
  const { userID = "" } = useParams()
  return <MobileEmployeeChatLookup key={userID} userID={userID} />
}

/** 确认成员有效并查找已有单聊，没有单聊时进入真人单聊草稿。 */
function MobileEmployeeChatLookup({ userID }: { userID: string }) {
  const { t } = useTranslation(["mobile", "common"])
  const { identity } = useMobileWorkspace()
  const location = useLocation()
  // 草稿路由编号在入口内保持稳定。
  const [draftRouteID] = useState(() => crypto.randomUUID())
  const profileURL = `/contacts/employees/${userID}`
  const member = useResource(resourceKeys.user(userID), () => getUser(userID), {
    staleTime: 0,
  })
  const user = member.data
  const canSend =
    user?.status === UserStatus.Active &&
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

  // 只在本次成员校验和会话查找完成后进入草稿，发送资格仍由服务端校验。
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
    return (
      <Navigate
        to={`/chats/direct/${draftRouteID}`}
        replace
        state={{
          ...location.state,
          draftPeer: { identityId: user.identityId, displayName: user.displayName },
          memberUserID: userID,
        } satisfies MobileIndividualLocationState}
      />
    )
  }

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

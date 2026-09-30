/** 解析通讯录传入的聊天对象并打开对应会话或草稿。 */
import { useEffect, useEffectEvent, useRef } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  findDirectConversation,
  isApiError,
  OrganizationIdentityType,
  sessionPath,
  type DirectInboxConversationData,
  type MemberOption,
} from "@/api"
import { listChatTargets } from "@/features/inbox/list-all-member-options"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"

/** 读取活跃身份，真人复用已有单聊，AI 始终进入新草稿。 */
export function InboxConversationTarget({
  identityId,
  currentIdentityId,
  onSelected,
  onFailed,
}: {
  identityId: string
  currentIdentityId: string
  onSelected: (
    member: MemberOption,
    conversation: DirectInboxConversationData | null,
  ) => void
  onFailed: () => void
}) {
  const { t } = useTranslation("inbox")
  const completedRef = useRef(false)
  const members = useResource(resourceKeys.chatTargets(), listChatTargets, {
    staleTime: 0,
  })
  const member = members.data?.find((item) => item.id === identityId)
  const direct =
    member?.type === OrganizationIdentityType.OrganizationIdentityTypeUser
  const lookup = useResource(
    resourceKeys.directConversation(identityId),
    () => findDirectConversation(identityId),
    { enabled: direct && identityId !== currentIdentityId, staleTime: 0 },
  )
  const error = members.error ?? lookup.error
  const pending =
    members.loading ||
    members.refreshing ||
    (direct && (lookup.loading || lookup.refreshing))
  const complete = useEffectEvent(() => {
    if (completedRef.current) return
    if (
      !error && member && identityId !== currentIdentityId && direct &&
      lookup.data === undefined
    ) return
    // 记录已落地的入口并复用对应草稿。
    completedRef.current = true
    if (error) {
      if (isApiError(error) && sessionPath(error.state)) return
      console.warn("打开通讯录聊天失败", { identityId, error })
      toast.error(
        isApiError(error)
          ? apiErrorMessage(error, ["targetIdentityId"])
          : t("directLookupError"),
      )
      onFailed()
    } else if (!member || identityId === currentIdentityId) {
      toast.error(t("chatTargetUnavailable"))
      onFailed()
    } else {
      onSelected(member, direct ? (lookup.data ?? null) : null)
    }
  })

  useEffect(() => {
    if (!pending && (members.data !== undefined || error)) complete()
  }, [pending, members.data, lookup.data, error])

  return null
}

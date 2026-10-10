/** 群聊免打扰开关的保存与待定显示。 */
import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import {
  isApiError,
  isNotFoundApiError,
  updateConversationNotificationSettings,
  type GroupConversation,
} from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useImmediateSave } from "@/hooks/use-immediate-save"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

/** 保存群聊免打扰，保存期间显示目标值，失败时恢复原值；会话不可访问时调用 onUnavailable。 */
export function useGroupMute(group: GroupConversation, onUnavailable?: () => void) {
  const { t } = useTranslation("inbox")
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const save = useImmediateSave()
  const [pending, setPending] = useState<boolean | null>(null)

  /** 保存免打扰开关，成功后刷新群资料、会话摘要与会话列表。 */
  async function change(muted: boolean) {
    const request = save.begin()
    if (request === null) return
    setPending(muted)
    try {
      await updateConversationNotificationSettings(group.id, { muted })
      await Promise.all([
        invalidate(resourceKeys.groupConversation(group.id)),
        invalidate(resourceKeys.conversationSummary(group.id)),
        invalidate(resourceKeys.inbox()),
      ])
    } catch (error) {
      if (!save.isCurrent(request) || recoverSession(error, navigate)) return
      if (isNotFoundApiError(error) && onUnavailable) onUnavailable()
      else {
        console.warn("保存群免打扰失败", { conversationID: group.id, error })
        toast.error(isApiError(error) ? apiErrorMessage(error) : t("conversationMuteError"))
      }
    } finally {
      if (save.isCurrent(request)) setPending(null)
      save.finish(request)
    }
  }

  return { muted: pending ?? group.muted, saving: save.saving, change }
}

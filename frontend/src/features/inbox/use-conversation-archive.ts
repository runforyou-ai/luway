/** 本人对群聊、单聊与 AI 聊天的归档切换。 */
import { useState } from "react"
import type { TFunction } from "i18next"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { isApiError, updateConversationArchive } from "@/api"
import { currentSessionGeneration } from "@/api/session-scope"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

/** 归档状态变化后需要重读的会话摘要、群资料、聊天列表、提醒数与已归档聊天。 */
function conversationArchiveKeys(conversationId: string) {
  return [
    resourceKeys.conversationSummary(conversationId),
    resourceKeys.groupConversation(conversationId),
    resourceKeys.inbox(),
    resourceKeys.inboxAttention(),
    resourceKeys.archivedConversations(),
  ]
}

/** 提示归档状态已保存；置顶会话说明已取消置顶，undo 非空时提供撤销并延长显示时间。 */
function announceArchiveChange(
  t: TFunction<["inbox", "common"]>,
  archived: boolean,
  pinned: boolean | null,
  undo: (() => void) | null,
) {
  if (!archived) {
    toast.success(t("conversationUnarchived"))
    return
  }
  toast.success(t("conversationArchived"), {
    description: t(pinned ? "conversationArchivedUnpinnedDescription" : "conversationArchivedDescription"),
    ...(undo ? { action: { label: t("common:actions.undo"), onClick: undo }, duration: 8000 } : {}),
  })
}

/** 保存会话的归档状态，保存后重读相关资源并提示结果，当前页面保持不变；按会话记录保存中状态，各会话的保存互不阻塞。 */
export function useConversationArchive() {
  const { t } = useTranslation(["inbox", "common"])
  const navigate = useNavigate()
  const invalidate = useResourceInvalidator()
  const [savingIDs, setSavingIDs] = useState<ReadonlySet<string>>(() => new Set())

  /**
   * 保存指定会话的归档状态，返回是否成功，失败时提示原因。
   * 归档同时取消置顶：pinned 为 false 时提示提供撤销，为 true 时说明已取消置顶，未知时传 null 且不提供撤销。
   * 结果与撤销只在发起时的登录会话中生效。
   */
  async function save(conversationId: string, archived: boolean, pinned: boolean | null = false) {
    const generation = currentSessionGeneration()
    setSavingIDs((current) => new Set(current).add(conversationId))
    try {
      await updateConversationArchive(conversationId, { archived })
      await Promise.all(conversationArchiveKeys(conversationId).map((key) => invalidate(key)))
      if (currentSessionGeneration() !== generation) return false
      announceArchiveChange(t, archived, pinned, pinned === false
        ? () => {
            if (currentSessionGeneration() === generation) void save(conversationId, false)
          }
        : null)
      return true
    } catch (error) {
      console.warn("更新会话归档失败", { conversationId, error })
      if (!recoverSession(error, navigate)) {
        toast.error(isApiError(error) ? apiErrorMessage(error) : t("conversationArchiveError"))
      }
      return false
    } finally {
      setSavingIDs((current) => {
        const next = new Set(current)
        next.delete(conversationId)
        return next
      })
    }
  }

  return {
    saving: savingIDs.size > 0,
    /** 返回指定会话是否正在保存归档状态。 */
    isSaving: (conversationId: string) => savingIDs.has(conversationId),
    save,
  }
}

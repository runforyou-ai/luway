/** AI 员工操作的确认、批准、拒绝与核对：进行中状态、错误提示与缓存失效。 */
import { useMutation } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"

import { decideAgentToolCall, reviewAgentToolCall } from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useConfirmedAction } from "@/hooks/use-confirmed-action"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResourceInvalidator } from "@/hooks/use-resource"

/** 处理操作所需的编号、名称与所在会话。 */
type ToolDecisionTarget = { id: string; name: string; conversationId: string }

/** 返回确认或批准、核对的执行方法，与拒绝的二次确认；timelineConversationId 是展示该操作的时间线所属会话，处理后与操作所在会话一并重读；onDone 在处理成功后回调。 */
export function useToolDecisionActions({
  timelineConversationId,
  onDone,
}: {
  timelineConversationId?: string
  onDone?: () => void
} = {}) {
  const { t } = useTranslation("agents")
  const reportError = useRequestErrorReporter()
  const invalidate = useResourceInvalidator()

  /** 重读待处理清单与相关会话消息，已被他人处理的操作随之更新。 */
  function refresh(item: ToolDecisionTarget) {
    const conversationIds = new Set([item.conversationId, timelineConversationId ?? item.conversationId])
    void invalidate(resourceKeys.agentToolDecisions())
    for (const id of conversationIds) {
      if (!id) continue
      void invalidate(resourceKeys.conversationMessages(id))
      void invalidate(resourceKeys.conversationMessagePage(id))
    }
  }

  // 确认或批准与核对无论成败都重读；成功后的 onDone 只在发起时的组件仍挂载时执行。
  const approval = useMutation({
    mutationFn: (item: ToolDecisionTarget) => decideAgentToolCall(item.id, { approve: true }),
    onError: (error, item) => reportError(error, { log: "确认或批准 AI 员工操作", context: { toolCallId: item.id }, fallback: t("toolDecisions.approveError") }),
    onSettled: (_data, _error, item) => refresh(item),
  })
  const review = useMutation({
    mutationFn: (item: ToolDecisionTarget) => reviewAgentToolCall(item.id),
    onError: (error, item) => reportError(error, { log: "核对 AI 员工操作", context: { toolCallId: item.id }, fallback: t("toolDecisions.reviewError") }),
    onSettled: (_data, _error, item) => refresh(item),
  })

  const rejection = useConfirmedAction<ToolDecisionTarget>({
    // 拒绝无论成败都重读，已被他人处理时展示最新状态。
    action: async (item) => {
      try {
        await decideAgentToolCall(item.id, { approve: false })
      } finally {
        refresh(item)
      }
    },
    logLabel: "拒绝 AI 员工操作",
    errorMessage: () => t("toolDecisions.reject.error"),
    onSuccess: () => onDone?.(),
  })

  return {
    rejection,
    /** 判断指定操作是否正在确认、批准或核对。 */
    isPending: (id: string) =>
      (approval.isPending && approval.variables?.id === id) || (review.isPending && review.variables?.id === id),
    /** 确认或批准操作。 */
    approve: (item: ToolDecisionTarget) => {
      if (!approval.isPending) approval.mutate(item, { onSuccess: () => onDone?.() })
    },
    /** 把结果待核对的操作标记为已核对。 */
    review: (item: ToolDecisionTarget) => {
      if (!review.isPending) review.mutate(item, { onSuccess: () => onDone?.() })
    },
  }
}

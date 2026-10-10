/** 待补知识处理结果的刷新与忽略操作。 */
import { useMutation } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"

import { dismissKnowledgeGap, type KnowledgeGap } from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResourceInvalidator } from "@/hooks/use-resource"

/** 返回刷新处理结果影响的清单、详情、报表与知识库问答的函数。 */
export function useKnowledgeGapRefresh() {
  const invalidate = useResourceInvalidator()
  return (gapId: string, knowledgeBaseId?: string) => {
    void Promise.all([
      invalidate(resourceKeys.knowledgeGaps()),
      invalidate(resourceKeys.knowledgeGap(gapId)),
      invalidate(resourceKeys.aiPerformanceReport()),
      knowledgeBaseId ? invalidate(resourceKeys.knowledgeQAEntries(knowledgeBaseId)) : undefined,
      invalidate(resourceKeys.agentEvaluation()),
    ])
  }
}

/** 提供忽略操作：成功后在处理内容仍显示时先交给下一条，再刷新相关读取。 */
export function useKnowledgeGapDismiss(gap: KnowledgeGap, onHandled: () => void) {
  const { t } = useTranslation("agents")
  const reportError = useRequestErrorReporter()
  const refresh = useKnowledgeGapRefresh()
  // 成功后刷新读取；处理内容仍挂载时由单次回调交给下一条。
  const dismissal = useMutation({
    mutationFn: (gapId: string) => dismissKnowledgeGap(gapId),
    onSuccess: (_, gapId) => refresh(gapId),
    onError: (error) => reportError(error, { log: "忽略待补知识", fallback: t("performance.gapSheet.dismissError") }),
  })
  const dismissing = dismissal.isPending

  /** 忽略该条目。 */
  function dismiss() {
    dismissal.mutate(gap.id, { onSuccess: () => onHandled() })
  }

  return { dismiss, dismissing }
}

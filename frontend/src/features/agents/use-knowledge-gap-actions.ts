/** 待补知识处理结果的刷新与忽略操作。 */
import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { dismissKnowledgeGap, isApiError, type KnowledgeGapData } from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

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
export function useKnowledgeGapDismiss(gap: KnowledgeGapData, onHandled: () => void) {
  const { t } = useTranslation("agents")
  const navigate = useNavigate()
  const refresh = useKnowledgeGapRefresh()
  const [dismissing, setDismissing] = useState(false)
  const mounted = useRef(false)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])

  /** 忽略该条目。 */
  async function dismiss() {
    setDismissing(true)
    try {
      await dismissKnowledgeGap(gap.id)
      // 处理内容已卸载时只刷新读取，不再切换条目。
      if (mounted.current) {
        setDismissing(false)
        onHandled()
      }
      refresh(gap.id)
    } catch (error) {
      if (mounted.current) setDismissing(false)
      if (recoverSession(error, navigate)) return
      console.warn("忽略待补知识失败", error)
      toast.error(isApiError(error) ? apiErrorMessage(error) : t("performance.gapSheet.dismissError"))
    }
  }

  return { dismiss, dismissing }
}

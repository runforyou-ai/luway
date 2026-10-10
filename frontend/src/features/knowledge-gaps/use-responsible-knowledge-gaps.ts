/** 本人负责的 AI 员工的待处理待补知识条数。 */
import { KnowledgeGapStatus, listKnowledgeGaps } from "@/api"
import { resourceKeys } from "@/hooks/resource-keys"
import { useResource } from "@/hooks/use-resource"

/** 读取本人负责的 AI 员工当前待处理的待补知识条数，服务会话变化时由同步协调器失效重读。 */
export function useResponsibleKnowledgeGapCount() {
  const gaps = useResource(resourceKeys.responsibleKnowledgeGapCount(), () =>
    listKnowledgeGaps({
      channelId: "",
      agentId: "",
      mine: true,
      status: KnowledgeGapStatus.Pending,
      page: 1,
      pageSize: 1,
    }),
  )
  return gaps.data?.page.total ?? 0
}

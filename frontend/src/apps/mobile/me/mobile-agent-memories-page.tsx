/** 移动端个人 AI 员工记忆：按最近更新列出记忆，逐条编辑或删除。 */
import { useRef } from "react"
import { BookmarkIcon, ChevronRightIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Link, Navigate, useParams } from "react-router"

import { useMobileBack } from "@/apps/mobile/shared/mobile-navigation"
import {
  MobilePageHeader,
  MobilePageState,
  MobileScrollArea,
} from "@/apps/mobile/shared/mobile-page"
import { ConfirmationDialog } from "@/components/confirmation-dialog"
import { LoadingIndicator } from "@/components/loading-indicator"
import { Button } from "@/components/ui/button"
import {
  AgentMemoryForm,
  useAgentMemories,
  useAgentMemoryDeletion,
} from "@/features/agents/personal/agent-memory-panel"
import { resourceKeys } from "@/hooks/resource-keys"
import { useDateTime } from "@/hooks/use-date-time"
import { useResourceInvalidator } from "@/hooks/use-resource"

/** 列出个人 AI 员工的记忆名称、说明与更新时间，点击进入编辑。 */
export function MobileAgentMemoriesPage() {
  const { t } = useTranslation(["agents", "common"])
  const { agentID = "" } = useParams()
  const { formatDateTime } = useDateTime()
  const { data, loading, error, refresh } = useAgentMemories(agentID)

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader
        title={t("personal.tabs.memory")}
        backTo={`/me/personal-agents/${agentID}`}
      />
      <MobileScrollArea storageKey={`agent-memories:${agentID}`} ready={Boolean(data)}>
        {data ? (
          data.memories.length ? (
            <ul className="divide-y border-b">
              {data.memories.map((memory) => (
                <li key={memory.id}>
                  <Link
                    to={`/me/personal-agents/${agentID}/memories/${memory.id}`}
                    state={{ mobileBack: true }}
                    className="flex min-h-18 items-center gap-3 px-4 py-3 outline-none active:bg-muted focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
                  >
                    <BookmarkIcon className="size-5 shrink-0 text-muted-foreground" />
                    <span className="grid min-w-0 flex-1 gap-0.5">
                      <span className="truncate text-[15px] font-medium">{memory.name}</span>
                      <span className="truncate text-xs text-muted-foreground">{memory.description}</span>
                      <span className="truncate text-xs text-muted-foreground">
                        {t("personal.memory.updatedAt", { time: formatDateTime(memory.updatedAt) })}
                      </span>
                    </span>
                    <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground" />
                  </Link>
                </li>
              ))}
            </ul>
          ) : (
            <MobilePageState title={t("personal.memory.empty")} />
          )
        ) : loading ? (
          <LoadingIndicator className="min-h-64 justify-center">
            {t("common:status.loading")}
          </LoadingIndicator>
        ) : error ? (
          <MobilePageState title={t("personal.memory.loadError")} onRetry={() => void refresh()} />
        ) : null}
      </MobileScrollArea>
    </section>
  )
}

/** 编辑地址中指定的记忆并提供删除；记忆已不存在时回到记忆列表，删除成功后返回来源页。 */
export function MobileAgentMemoryPage() {
  const { t } = useTranslation(["agents", "common"])
  const { agentID = "", memoryID = "" } = useParams()
  const listPath = `/me/personal-agents/${agentID}/memories`
  const back = useMobileBack(listPath)
  const invalidate = useResourceInvalidator()
  const { data, loading, error, refresh } = useAgentMemories(agentID)
  const deleted = useRef(false)
  const deletion = useAgentMemoryDeletion(agentID, () => {
    deleted.current = true
    back()
  })
  const memory = data?.memories.find((item) => item.id === memoryID)

  // 删除成功后由返回离开，列表刷新先到达时也不另行跳转。
  if (data && !memory && !deleted.current) {
    return <Navigate to={listPath} replace />
  }

  return (
    <section className="flex h-full min-h-0 flex-col">
      <MobilePageHeader title={t("personal.memory.edit")} backTo={listPath} />
      <div className="app-form min-h-0 flex-1 overflow-y-auto overscroll-contain p-4">
        {memory ? (
          <div className="space-y-3">
            <AgentMemoryForm
              key={memory.id}
              agentId={agentID}
              memory={memory}
              onSaved={() => {
                void invalidate(resourceKeys.agentMemories(agentID))
                back()
              }}
            />
            <Button
              type="button"
              variant="destructive"
              className="min-h-11 w-full"
              onClick={() => deletion.select(memory)}
            >
              {t("common:actions.delete")}
            </Button>
            <ConfirmationDialog
              {...deletion.dialog}
              title={t("personal.memory.deleteTitle", { name: deletion.item?.name ?? "" })}
              description={t("personal.memory.deleteDescription")}
              pendingLabel={t("common:actions.deleting")}
            />
          </div>
        ) : loading ? (
          <LoadingIndicator className="min-h-64 justify-center">
            {t("common:status.loading")}
          </LoadingIndicator>
        ) : error ? (
          <MobilePageState title={t("personal.memory.loadError")} onRetry={() => void refresh()} />
        ) : null}
      </div>
    </section>
  )
}

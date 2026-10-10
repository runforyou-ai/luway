/** 待处理页按地址中的 decision 参数核验链接指向的操作是否仍待处理。 */
import { useEffect, useRef } from "react"

/** 链接指向的操作不在清单中时先重读一次，打开链接后读到的清单仍没有才调用 onUnavailable；链接变化时重新核验，重读失败时不调用。 */
export function useDecisionLink({
  decisionID,
  loaded,
  found,
  refreshing,
  updatedAt,
  refresh,
  onUnavailable,
}: {
  decisionID: string | null
  loaded: boolean
  found: boolean
  refreshing: boolean
  updatedAt: number
  refresh: () => unknown
  onUnavailable: () => void
}) {
  const check = useRef<{ id: string; openedAt: number; refreshRequested: boolean } | null>(null)
  const unavailable = useRef(onUnavailable)
  unavailable.current = onUnavailable
  useEffect(() => {
    if (!decisionID || !loaded || found || refreshing) return
    if (check.current?.id !== decisionID) {
      check.current = { id: decisionID, openedAt: Date.now(), refreshRequested: false }
    }
    const current = check.current
    if (updatedAt < current.openedAt) {
      // 每个链接只主动重读一次，重读失败时保留链接，不误报已处理。
      if (!current.refreshRequested) {
        current.refreshRequested = true
        void refresh()
      }
      return
    }
    unavailable.current()
  }, [decisionID, loaded, found, refreshing, updatedAt, refresh])
}

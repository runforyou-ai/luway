/** 按记录编号跟踪进行中的行操作。 */
import { useState } from "react"

/** 返回进行中的编号集合，以及在任务期间登记编号的执行方法。 */
export function usePendingIds() {
  const [pendingIds, setPendingIds] = useState<ReadonlySet<string>>(new Set())

  /** 登记编号后执行任务，结束时移除登记。 */
  async function run<T>(id: string, task: () => Promise<T>) {
    setPendingIds((current) => new Set(current).add(id))
    try {
      return await task()
    } finally {
      setPendingIds((current) => {
        const next = new Set(current)
        next.delete(id)
        return next
      })
    }
  }

  return { pendingIds, run }
}

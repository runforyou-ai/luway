/** 管理需要二次确认的单条记录操作：确认对象、请求状态、提示和缓存失效。 */
import { useState } from "react"
import type { QueryKey } from "@tanstack/react-query"
import { toast } from "sonner"

import { useImmediateSave } from "@/hooks/use-immediate-save"
import { useRequestErrorReporter } from "@/hooks/use-request-error-reporter"
import { useResourceInvalidator } from "@/hooks/use-resource"

/** 串行执行确认后的操作，成功后失效相关缓存、关闭确认并把操作结果交给 onSuccess，失败时保留确认上下文。 */
export function useConfirmedAction<T, R = unknown>({
  action,
  invalidateKeys = () => [],
  successMessage,
  errorMessage,
  logLabel,
  onSuccess,
}: {
  action: (item: T) => Promise<R>
  invalidateKeys?: (item: T) => QueryKey[]
  successMessage?: (item: T) => string
  errorMessage: (item: T) => string
  logLabel: string
  onSuccess?: (item: T, result: R) => void
}) {
  const [item, setItem] = useState<T | null>(null)
  const save = useImmediateSave()
  const invalidate = useResourceInvalidator()
  const reportError = useRequestErrorReporter()

  /** 执行当前确认对象的操作，离开页面后仅更新共享缓存。 */
  async function confirm() {
    if (item === null) return
    const request = save.begin()
    if (request === null) return
    try {
      const result = await action(item)
      for (const key of invalidateKeys(item)) void invalidate(key)
      if (!save.isCurrent(request)) return
      setItem(null)
      if (successMessage) toast.success(successMessage(item))
      onSuccess?.(item, result)
    } catch (error) {
      if (!save.isCurrent(request)) return
      reportError(error, { log: logLabel, context: { item }, fallback: errorMessage(item) })
    } finally {
      save.finish(request)
    }
  }

  return {
    item,
    select: setItem,
    pending: save.saving,
    confirm,
    /** 展开到 ConfirmationDialog 的开关、进行中状态和确认回调，标题与说明由调用方给出。 */
    dialog: {
      open: item !== null,
      pending: save.saving,
      onOpenChange: (open: boolean) => {
        if (!open) setItem(null)
      },
      onConfirm: () => void confirm(),
    },
  }
}

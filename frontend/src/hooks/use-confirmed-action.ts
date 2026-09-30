/** 管理需要二次确认的单条记录操作：确认对象、请求状态、提示和缓存失效。 */
import { useState } from "react"
import type { QueryKey } from "@tanstack/react-query"
import { useNavigate } from "react-router"
import { toast } from "sonner"

import { isApiError } from "@/api"
import { useImmediateSave } from "@/hooks/use-immediate-save"
import { useResourceInvalidator } from "@/hooks/use-resource"
import { apiErrorMessage } from "@/lib/form-errors"
import { recoverSession } from "@/lib/session-navigation"

/** 串行执行确认后的操作，成功后失效相关缓存并关闭确认，失败时保留确认上下文。 */
export function useConfirmedAction<T>({
  action,
  invalidateKeys = () => [],
  successMessage,
  errorMessage,
  logLabel,
  onSuccess,
}: {
  action: (item: T) => Promise<unknown>
  invalidateKeys?: (item: T) => QueryKey[]
  successMessage?: (item: T) => string
  errorMessage: (item: T) => string
  logLabel: string
  onSuccess?: (item: T) => void
}) {
  const [item, setItem] = useState<T | null>(null)
  const save = useImmediateSave()
  const invalidate = useResourceInvalidator()
  const navigate = useNavigate()

  /** 执行当前确认对象的操作，离开页面后仅更新共享缓存。 */
  async function confirm() {
    if (item === null) return
    const request = save.begin()
    if (request === null) return
    try {
      await action(item)
      for (const key of invalidateKeys(item)) void invalidate(key)
      if (!save.isCurrent(request)) return
      setItem(null)
      if (successMessage) toast.success(successMessage(item))
      onSuccess?.(item)
    } catch (error) {
      if (!save.isCurrent(request) || recoverSession(error, navigate)) return
      console.warn(`${logLabel}失败`, { item, error })
      toast.error(isApiError(error) ? apiErrorMessage(error) : errorMessage(item))
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

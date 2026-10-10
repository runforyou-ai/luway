/** 登记表单未保存状态并标识组件生命周期。 */
import { useEffect, useRef } from "react"
import { useLocation } from "react-router"

import { useUnsavedChangesContext } from "@/contexts/unsaved-changes-context"

/** 保留未保存提示，并让提交操作忽略卸载后的界面更新；discarded 表示用户已确认放弃当前修改。 */
export function useFormLifetime(isDirty: boolean) {
  const { pathname } = useLocation()
  const context = useUnsavedChangesContext()
  const mounted = useRef(false)
  const dirty = useRef(isDirty)
  dirty.current = isDirty
  const discarded = useRef(false)
  const register = context?.register

  useEffect(() => {
    mounted.current = true
    discarded.current = false
    const unregister = register?.(Symbol(), { pathname, dirty, discarded })
    return () => {
      mounted.current = false
      unregister?.()
    }
  }, [pathname, register])

  return { mounted, dirty, discarded }
}

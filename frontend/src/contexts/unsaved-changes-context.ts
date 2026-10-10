/** 共享表单未保存状态登记入口。 */
import { createContext, useContext, type RefObject } from "react"

/** 登记的表单：dirty 表示有未保存内容，用户确认放弃后由导航守卫把 discarded 置为 true。 */
export type UnsavedForm = {
  pathname: string
  dirty: RefObject<boolean>
  discarded: RefObject<boolean>
}

export const UnsavedChangesContext = createContext<{
  register: (id: symbol, form: UnsavedForm) => () => void
  confirmDiscard: () => Promise<boolean>
} | null>(null)

/** 读取当前工作台的未保存内容管理入口。 */
export function useUnsavedChangesContext() {
  return useContext(UnsavedChangesContext)
}

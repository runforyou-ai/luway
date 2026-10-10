/** 新增或编辑弹窗的打开对象与弹窗会话。 */
import { useRef, useState } from "react"

/** 返回当前编辑对象与开关方法；item 为空表示新增，保存完成时 finish 只关闭仍是同一会话的弹窗。 */
export function useEditingDialog<T>() {
  const [editing, setEditing] = useState<{ item?: T; session: number } | null>(null)
  const session = useRef(0)

  /** 打开新增或编辑弹窗，并开始新的弹窗会话。 */
  function open(item?: T) {
    session.current += 1
    setEditing({ item, session: session.current })
  }

  /** 关闭弹窗。 */
  function close() {
    setEditing(null)
  }

  /** 保存完成后关闭发起保存时的弹窗；保存期间弹窗已关闭并重新打开时保留新弹窗。 */
  function finish(opened: { session: number } | null) {
    if (opened?.session === session.current) setEditing(null)
  }

  return { editing, open, close, finish }
}

/** 管理详情字段即时保存的并发状态。 */
import { useEffect, useRef, useState } from "react"

/** 串行执行保存，并忽略组件卸载后的结果。 */
export function useImmediateSave() {
  const [saving, setSaving] = useState(false)
  const savingRef = useRef(false)
  const requestRef = useRef(0)

  useEffect(
    () => () => {
      requestRef.current += 1
      savingRef.current = false
    },
    [],
  )

  /** 开始保存。 */
  function begin() {
    if (savingRef.current) return null
    savingRef.current = true
    setSaving(true)
    requestRef.current += 1
    return requestRef.current
  }

  /** 判断保存结果是否仍然有效。 */
  function isCurrent(request: number) {
    return requestRef.current === request
  }

  /** 结束保存。 */
  function finish(request: number) {
    if (!isCurrent(request)) return
    savingRef.current = false
    setSaving(false)
  }

  return {
    saving,
    isSaving: () => savingRef.current,
    begin,
    isCurrent,
    finish,
  }
}

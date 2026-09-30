/** 标识组件是否仍处于挂载状态。 */
import { useEffect, useRef } from "react"

/** 返回挂载期间为 true 的 ref，异步操作完成后据此忽略已卸载组件的界面更新。 */
export function useMountedRef() {
  const mounted = useRef(false)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  return mounted
}

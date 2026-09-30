/** Web、桌面端和移动端共用的页面全选快捷键控制。 */
import { useEffect } from "react"

/** 判断快捷键是否作用于可编辑文本控件。 */
function isTextEditingTarget(target: EventTarget | null) {
  return (
    target instanceof HTMLInputElement ||
    target instanceof HTMLTextAreaElement ||
    (target instanceof HTMLElement && target.isContentEditable)
  )
}

/** 阻止非编辑区域执行网页整页全选。 */
export function usePreventPageSelectAll() {
  useEffect(() => {
    /** 阻止非编辑区域的全选默认行为。 */
    function preventPageSelectAll(event: KeyboardEvent) {
      if (
        event.key.toLowerCase() === "a" &&
        (event.ctrlKey || event.metaKey) &&
        !event.altKey &&
        !event.shiftKey &&
        !isTextEditingTarget(event.target)
      ) {
        event.preventDefault()
      }
    }

    window.addEventListener("keydown", preventPageSelectAll, true)
    return () =>
      window.removeEventListener("keydown", preventPageSelectAll, true)
  }, [])
}

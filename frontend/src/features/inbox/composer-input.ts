/** 消息输入框的高度自适应与键入转交焦点。 */
import { useEffect, type RefObject } from "react"

const conversationComposerMaxHeight = 200

/** 根据文本内容调整消息输入框高度。 */
export function resizeComposerInput(input: HTMLTextAreaElement | null) {
  if (!input) return
  input.style.height = "auto"
  input.style.height = `${Math.min(input.scrollHeight, conversationComposerMaxHeight)}px`
  const renderedHeight = input.getBoundingClientRect().height
  input.style.overflowY = input.scrollHeight > renderedHeight ? "auto" : "hidden"
}

/** 输入框可用时，焦点不在输入控件上按下的可打印字符转交输入框。 */
export function useFocusInputOnTyping(
  inputRef: RefObject<HTMLTextAreaElement | null>,
  enabled: boolean,
) {
  useEffect(() => {
    if (!enabled) return
    // 焦点不在输入控件上时，按下可打印字符直接转交输入框，该字符落入输入框。
    function focusFromTyping(event: globalThis.KeyboardEvent) {
      if (event.defaultPrevented || event.isComposing) return
      if (event.ctrlKey || event.metaKey || event.altKey) return
      // 空格是按钮和复选框的激活键，留给当前焦点元素。
      if (event.key.length !== 1 || event.key === " ") return
      const input = inputRef.current
      if (!input || input.closest("[aria-hidden='true']")) return
      const active = document.activeElement
      if (
        active instanceof HTMLElement &&
        (active.isContentEditable ||
          active.tagName === "INPUT" ||
          active.tagName === "TEXTAREA" ||
          active.tagName === "SELECT" ||
          // 对话框和各类浮层内的按键归浮层处理。
          active.closest("[data-radix-popper-content-wrapper],[role='dialog']"))
      )
        return
      input.focus()
      input.setSelectionRange(input.value.length, input.value.length)
    }
    document.addEventListener("keydown", focusFromTyping)
    return () => document.removeEventListener("keydown", focusFromTyping)
  }, [enabled, inputRef])
}

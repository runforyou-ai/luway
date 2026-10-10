/** 对话框和侧边面板打开时的初始焦点规则。 */

/** 弹窗内除关闭按钮和链接外没有可聚焦控件时，把初始焦点放在弹窗容器上；有控件时保留默认的首个控件聚焦。 */
export function focusContainerWithoutControls(event: Event) {
  const container = event.currentTarget
  if (event.defaultPrevented || !(container instanceof HTMLElement)) return
  // 候选规则与 Radix 默认聚焦一致：可 Tab 到达、未禁用、可见且不是链接。
  const hasControl = Array.from(container.querySelectorAll<HTMLElement>("*")).some(
    (element) =>
      element.tabIndex >= 0 &&
      element.tagName !== "A" &&
      !element.matches(":disabled, input[type=hidden], [data-dialog-close]") &&
      element.getClientRects().length > 0,
  )
  if (hasControl) return
  event.preventDefault()
  container.focus({ preventScroll: true })
}

/** 把初始焦点放在弹窗容器上，用于打开时不应自动聚焦任何控件的弹窗。 */
export function focusDialogContainer(event: Event) {
  const container = event.currentTarget
  if (!(container instanceof HTMLElement)) return
  event.preventDefault()
  container.focus({ preventScroll: true })
}

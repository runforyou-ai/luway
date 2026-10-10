/** 按可用宽度从中间省略附件名称，保留文件名首尾。 */
import { useLayoutEffect, useRef, useState } from "react"

/** 随容器宽度变化计算单行文件名。 */
export function AttachmentName({ name }: { name: string }) {
  const ref = useRef<HTMLSpanElement>(null)
  const [displayName, setDisplayName] = useState(name)

  useLayoutEffect(() => {
    const element = ref.current
    const context = document.createElement("canvas").getContext("2d")
    if (!element || !context) return
    const characters = Array.from(name)

    /** 保留能放入当前宽度的最长文件名首尾组合。 */
    const updateName = () => {
      context.font = getComputedStyle(element).font
      const width = element.clientWidth
      if (context.measureText(name).width <= width) {
        setDisplayName(name)
        return
      }
      let low = 0
      let high = characters.length
      let result = "…"
      while (low <= high) {
        const count = Math.floor((low + high) / 2)
        const candidate =
          characters.slice(0, Math.ceil(count / 2)).join("") +
          "…" +
          characters.slice(characters.length - Math.floor(count / 2)).join("")
        if (context.measureText(candidate).width <= width) {
          result = candidate
          low = count + 1
        } else {
          high = count - 1
        }
      }
      setDisplayName(result)
    }

    updateName()
    const observer = new ResizeObserver(updateName)
    observer.observe(element)
    return () => observer.disconnect()
  }, [name])

  return (
    <span ref={ref} className="block truncate">
      {displayName}
    </span>
  )
}

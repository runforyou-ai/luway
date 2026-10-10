/** 把链接生成为二维码图片，二维码库在首次生成时按需加载。 */
import { useEffect, useState } from "react"

/** 返回 text 的二维码数据地址与失败状态；text 为空或 enabled 为 false 时不生成，retry 重新生成。 */
export function useQRCode(text: string, enabled = true) {
  const [result, setResult] = useState({ text: "", dataURL: "", failed: false })
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    if (!enabled || !text) return
    let active = true
    void import("qrcode")
      .then((QRCode) =>
        QRCode.toDataURL(text, {
          width: 160,
          margin: 1,
          errorCorrectionLevel: "M",
          color: { dark: "#111827", light: "#FFFFFF" },
        }),
      )
      .then((dataURL) => {
        if (active) setResult({ text, dataURL, failed: false })
      })
      .catch((error: unknown) => {
        if (!active) return
        console.warn("二维码生成失败", error)
        setResult({ text, dataURL: "", failed: true })
      })
    return () => {
      active = false
    }
  }, [text, enabled, attempt])

  // 只返回与当前 text 对应的结果，链接变化后旧图随即清空。
  const current = result.text === text ? result : { dataURL: "", failed: false }
  return {
    dataURL: current.dataURL,
    failed: current.failed,
    retry: () => {
      setResult({ text: "", dataURL: "", failed: false })
      setAttempt((value) => value + 1)
    },
  }
}

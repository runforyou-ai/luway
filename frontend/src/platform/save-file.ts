/** 按平台把文本内容保存为本机文件。 */
import { saveNativeTextFile } from "@/api"
import { resolveAppPlatform } from "@/platform/app-platform"

/** 下载链接的保留时长，浏览器读取完成后再释放。 */
const objectURLLifetime = 40_000

/** Web 端以建议文件名下载文本，原生端打开保存对话框；返回是否已保存，原生端用户取消时为 false。 */
export async function saveTextFile(name: string, content: string, type: string) {
  if (resolveAppPlatform() !== "web") return saveNativeTextFile({ name, content })
  const url = URL.createObjectURL(new Blob([content], { type }))
  const anchor = document.createElement("a")
  anchor.href = url
  anchor.download = name
  document.body.append(anchor)
  anchor.click()
  anchor.remove()
  setTimeout(() => URL.revokeObjectURL(url), objectURLLifetime)
  return true
}

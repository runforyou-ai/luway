/** 下载能力：下载服务端签发的文件地址与把文本保存为本机文件，启动时按平台选定 Web 或原生端实现。 */
import { selectPlatformImplementation } from "@/platform/app-platform"
import { saveNativeTextFile } from "@/platform/native"
import { openExternalURL } from "@/platform/system"

/** 文本下载链接的保留时长，浏览器读取完成后再释放。 */
const objectURLLifetime = 40_000

/** 各平台提供的下载能力。 */
type Downloads = {
  /** 下载服务端签发的文件地址，name 为建议文件名。 */
  downloadURL: (url: string, name: string) => Promise<void>
  /** 把文本内容保存为本机文件，返回是否已保存，原生端用户取消时为 false。 */
  saveTextFile: (name: string, content: string, type: string) => Promise<boolean>
}

/** 以带建议文件名的链接触发浏览器下载。 */
function clickDownloadLink(href: string, name: string) {
  const anchor = document.createElement("a")
  anchor.href = href
  anchor.download = name
  document.body.append(anchor)
  anchor.click()
  anchor.remove()
}

/** Web 端由浏览器按建议文件名下载。 */
const webDownloads: Downloads = {
  downloadURL: async (url, name) => clickDownloadLink(url, name),
  saveTextFile: async (name, content, type) => {
    const url = URL.createObjectURL(new Blob([content], { type }))
    clickDownloadLink(url, name)
    setTimeout(() => URL.revokeObjectURL(url), objectURLLifetime)
    return true
  },
}

/** 原生端把文件地址交给系统浏览器下载，文本经保存对话框写入。 */
const nativeDownloads: Downloads = {
  downloadURL: (url) => openExternalURL(url),
  saveTextFile: (name, content) => saveNativeTextFile({ name, content }),
}

// 当前平台的下载能力。
export const { downloadURL, saveTextFile } = selectPlatformImplementation({ web: webDownloads, native: nativeDownloads })

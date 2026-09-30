/** 按平台在系统浏览器中打开外部链接。 */
import { resolveAppPlatform } from "@/platform/app-platform"

/** 按平台在系统浏览器中打开外部 URL。 */
export async function openExternalURL(url: string) {
  if (resolveAppPlatform() === "web") {
    window.open(url, "_blank", "noopener,noreferrer")
    return
  }

  const { Browser } = await import("@wailsio/runtime")
  await Browser.OpenURL(url)
}

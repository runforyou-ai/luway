/** 识别当前运行平台。 */

export type AppPlatform = "web" | "desktop" | "mobile"

type DesktopOS = "darwin" | "windows" | "linux"

type WailsWindow = Window & {
  _wails?: {
    environment?: {
      OS?: string
    }
  }
  webkit?: {
    messageHandlers?: {
      external?: {
        postMessage?: unknown
      }
    }
  }
  chrome?: {
    webview?: {
      postMessage?: unknown
    }
  }
  wails?: {
    invoke?: unknown
  }
}

let cachedPlatform: AppPlatform | null = null

/** 返回当前应用平台；平台在启动时确定，首次识别后缓存结果。 */
export function resolveAppPlatform(): AppPlatform {
  cachedPlatform ??= detectAppPlatform()
  return cachedPlatform
}

/** 根据运行环境和开发预览标记识别应用平台。 */
function detectAppPlatform(): AppPlatform {
  // 开发窗口通过地址标记加载移动端页面和交互。
  if (import.meta.env.DEV && new URLSearchParams(window.location.search).get("preview") === "mobile") {
    return "mobile"
  }
  const wailsWindow = window as WailsWindow
  const os = wailsWindow._wails?.environment?.OS
  if (os === "ios" || os === "android") {
    return "mobile"
  }
  if (os === "darwin" || os === "windows" || os === "linux") {
    return "desktop"
  }

  const userAgent = navigator.userAgent.toLowerCase()
  const hasWebKitBridge =
    typeof wailsWindow.webkit?.messageHandlers?.external?.postMessage ===
    "function"
  const hasWindowsBridge =
    typeof wailsWindow.chrome?.webview?.postMessage === "function"
  const hasAndroidBridge = typeof wailsWindow.wails?.invoke === "function"

  if (
    hasAndroidBridge ||
    (hasWebKitBridge && /iphone|ipad|ipod/.test(userAgent))
  ) {
    return "mobile"
  }
  if (hasWindowsBridge || hasWebKitBridge) {
    return "desktop"
  }
  return "web"
}

/** 识别桌面端运行的操作系统。 */
export function resolveDesktopOS(): DesktopOS | null {
  if (resolveAppPlatform() !== "desktop") {
    return null
  }
  const wailsWindow = window as WailsWindow
  const os = wailsWindow._wails?.environment?.OS
  if (os === "darwin" || os === "windows" || os === "linux") {
    return os
  }
  if (typeof wailsWindow.chrome?.webview?.postMessage === "function") {
    return "windows"
  }
  return /macintosh|mac os x/i.test(navigator.userAgent) ? "darwin" : "linux"
}

/** 判断桌面端是否运行在 macOS。 */
export function isDesktopMacOS(): boolean {
  return resolveDesktopOS() === "darwin"
}
